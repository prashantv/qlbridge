package schema

import (
	"database/sql/driver"
	"fmt"
	"hash/fnv"
	"sort"
	"strings"
	"time"

	u "github.com/araddon/gou"

	"github.com/araddon/qlbridge/expr"
	"github.com/araddon/qlbridge/value"
)

var (
	_ = u.EMPTY

	// default schema Refresh Interval
	SchemaRefreshInterval = -time.Minute * 5

	// Static list of common field names for describe header
	// - full columns shows all,
	// - "describe table" only shows sub-set
	DescribeFullCols    = []string{"Field", "Type", "Collation", "Null", "Key", "Default", "Extra", "Privileges", "Comment"}
	DescribeCols        = []string{"Field", "Type", "Null", "Key", "Default", "Extra"}
	DescribeFullHeaders = NewDescribeFullHeaders()
	DescribeHeaders     = NewDescribeHeaders()

	// We use Fields, and Tables as messages in Schema (SHOW, DESCRIBE)
	_ Message = (*Field)(nil)
	_ Message = (*Table)(nil)
)

const (
	NoNulls    = false
	AllowNulls = true
)

type (
	// Schema is a "Virtual" Schema Database.  Made up of
	//  - Multiple DataSource(s) (each may be discrete source type)
	//  - each datasource supplies tables to the virtual table pool
	//  - each table from each source must be unique (or aliased)
	Schema struct {
		Name          string                   `json:"name"`
		SourceSchemas map[string]*SourceSchema // map[source_name]:Source Schemas
		tableSources  map[string]*SourceSchema // Tables to source map
		tableMap      map[string]*Table        // Tables and their field info, flattened from all sources
		tableNames    []string                 // List Table names, flattened all sources into one list
		lastRefreshed time.Time                // Last time we refreshed this schema
	}

	// SourceSchema is a schema for a single DataSource (elasticsearch, mysql, filesystem, elasticsearch)
	//  each DataSource would have multiple tables
	SourceSchema struct {
		Name       string            // Source specific Schema name, generally underlying db name
		Conf       *SourceConfig     // source configuration
		Schema     *Schema           // Schema this is participating in
		Nodes      []*NodeConfig     // List of nodes config
		DS         DataSource        // This datasource Interface
		tableMap   map[string]*Table // Tables from this Source
		tableNames []string          // List Table names
		address    string
	}

	// Table represents traditional definition of Database Table
	//   It belongs to a Schema and can be used to
	//   create a Datasource used to read this table
	Table struct {
		Name           string            // Name of table lowercased
		NameOriginal   string            // Name of table
		FieldPositions map[string]int    // Maps name of column to ordinal position in array of []driver.Value's
		Fields         []*Field          // List of Fields, in order
		FieldMap       map[string]*Field // List of Fields, in order
		Schema         *Schema           // The schema this is member of
		SourceSchema   *SourceSchema     // The source schema this is member of
		Charset        uint16            // Character set, default = utf8
		tblId          uint64            // internal tableid, hash of table name + schema?
		cols           []string          // array of column names
		lastRefreshed  time.Time         // Last time we refreshed this schema
		//DescribeValues [][]driver.Value  // The Values that will be output for Describe
	}

	// Field Describes the column info, name, data type, defaults, index, null
	//  - dialects (mysql, mongo, cassandra) have their own descriptors for these,
	//    so this is generic meant to be converted to Frontend at runtime
	Field struct {
		idx                uint64          // Positional index in array of fields
		Name               string          // Column Name
		Description        string          // Comment/Description
		Key                string          // Key info (primary, etc) should be stored in indexes
		Extra              string          // no idea difference with Description
		Data               FieldData       // Pre-generated dialect specific data???
		Length             uint32          // field-size, ie varchar(20)
		Type               value.ValueType // Value type, there needs to be dialect specific converters
		DefaultValueLength uint64          // Default
		DefaultValue       driver.Value    // Default value
		Indexed            bool            // Is this indexed, if so we will have a list of indexes
		NoNulls            bool            // Do we allow nulls?  default = false = yes allow nulls
		Collation          string          // ie, utf8, none
		Roles              []string        // ie, {select,insert,update,delete}
		Indexes            []*Index
	}
	FieldData []byte

	// Describe an Index
	Index struct {
		Fields []string
		// ??? Primary?  hashed?  btree? partition?  unique?
	}

	// A SchemaConfig defines the data-sources that make up this Virtual Schema
	//  - config to map name to multiple sources
	//  - connection info
	SchemaConfig struct {
		Name       string   `json:"name"`    // Virtual Schema Name, must be unique
		Sources    []string `json:"sources"` // List of sources , the names of the "Db" in source
		NodeConfig []string `json:"-"`       // List of backend Servers
	}

	// Config for Source are storage/database/csvfiles
	//  - this represents a single source type
	//  - may have more than one node
	//  - belongs to one or more virtual schemas
	SourceConfig struct {
		Name         string        `json:"name"`           // Name
		SourceType   string        `json:"type"`           // [mysql,elasticsearch,csv,etc] Name in DataSource Registry
		TablesToLoad []string      `json:"tables_to_load"` // if non empty, only load these tables
		Nodes        []*NodeConfig `json:"nodes"`          // List of nodes
		Settings     u.JsonHelper  `json:"settings"`       // Arbitrary settings specific to each source type
	}

	// Nodes are Servers
	//  - this represents a single source type
	//  - may have config info in Settings such as
	//     - user     = username
	//     - password = password
	//     - # connections
	NodeConfig struct {
		Name     string       `json:"name"`     // Name of this Node optional
		Source   string       `json:"source"`   // Name of source this node belongs to
		Address  string       `json:"address"`  // host/ip
		Settings u.JsonHelper `json:"settings"` // Arbitrary settings
	}
)

func NewSchema(schemaName string) *Schema {
	m := &Schema{
		Name:          strings.ToLower(schemaName),
		SourceSchemas: make(map[string]*SourceSchema),
		tableMap:      make(map[string]*Table),
		tableSources:  make(map[string]*SourceSchema),
		tableNames:    make([]string, 0),
	}
	return m
}

func (m *Schema) RefreshSchema() {
	//u.Debugf("refresh %#v", m.SourceSchemas)
	for _, ss := range m.SourceSchemas {
		if ss.DS == nil {
			for _, tableName := range ss.Tables() {
				//u.Infof("tableName %s", tableName)
				ss.AddTableName(tableName)
				m.AddTableName(tableName, ss)
			}
			return
		}
		for _, tableName := range ss.DS.Tables() {
			//u.Infof("tableName %s", tableName)
			ss.AddTableName(tableName)
			m.AddTableName(tableName, ss)
		}
	}
}

func (m *Schema) AddSourceSchema(ss *SourceSchema) {
	m.SourceSchemas[ss.Name] = ss
	m.RefreshSchema()
}
func (m *Schema) Source(tableName string) (*SourceSchema, error) {
	//u.Debugf("%p Schema Source() %q %v", m, tableName, m.tableSources)
	ss, ok := m.tableSources[tableName]

	if ok && ss != nil && ss.DS != nil {
		//u.Infof("%p %p  found? %v  ss=%#v", m, ss, ok, ss)
		return ss, nil
	}
	if ok && ss != nil && ss.DS == nil {

	} else {
		ss, ok = m.tableSources[strings.ToLower(tableName)]
		if ok && ss != nil {
			return ss, nil
		}
	}

	// If a table source has been added since we built this
	// internal schema table cache, it may be missing so try to refresh it
	for _, ss2 := range m.SourceSchemas {
		for _, tbl := range ss2.DS.Tables() {
			if _, exists := m.tableSources[tbl]; !exists {
				//m.tableSources[tbl] = ss
				//u.Debugf("%p Schema  new table? %s:%v", ss2.Schema, sourceName, tbl)
				ss2.Schema.RefreshSchema()
				return ss2, nil
			} else if tbl == tableName {
				//u.Warnf("WHAT?  we should have a DS on tableSources?")
				if ss != nil {
					ss.DS = ss2.DS
				}
				//ss.DS = ss2.DS
				return ss2, nil
			}
		}
	}
	return nil, fmt.Errorf("Could not find a source for that table %q", tableName)
}

// Get a connection from this source via table name
func (m *Schema) Open(tableName string) (SourceConn, error) {
	source, err := m.Source(tableName)
	if err != nil {
		//u.Warnf("%p could not find? %v", m, err)
		//u.LogTracef(u.WARN, "hello")
		return nil, err
	}
	if source.DS == nil {
		//u.Warnf("%p Schema no table? %v", m, tableName)
		return nil, fmt.Errorf("Could not find a DataSource for that table %q", tableName)
	}

	conn, err := source.DS.Open(tableName)
	if err != nil {
		return nil, err
	}
	if conn == nil {
		return nil, fmt.Errorf("Could not establish a connection for %v", tableName)
	}
	return conn, nil
}

// Is this schema uptodate?
func (m *Schema) Current() bool    { return m.Since(SchemaRefreshInterval) }
func (m *Schema) Tables() []string { return m.tableNames }
func (m *Schema) Table(tableName string) (*Table, error) {
	tbl, ok := m.tableMap[tableName]
	if ok && tbl != nil {
		return tbl, nil
	}
	_, tableName, _ = expr.LeftRight(tableName)
	return m.findTable(strings.ToLower(tableName))
}
func (m *Schema) findTable(tableName string) (*Table, error) {
	tbl, ok := m.tableMap[tableName]

	if ok && tbl != nil {
		return tbl, nil
	} else if !ok || tbl == nil {
		//u.Warnf("%p Schema  %v  tableMap:%v", m, m.tableSources, m.tableMap)
		if ss, ok := m.tableSources[tableName]; ok {
			//u.Infof("try to get table from source schema %v", tableName)
			if sourceTable, ok := ss.DS.(SchemaProvider); ok {
				tbl, err := sourceTable.Table(tableName)
				if tbl == nil {
					//u.Warnf("nil table? %v source:%#v", tableName, sourceTable)
				}
				if err == nil {
					m.addTable(tbl)
				}
				return tbl, err
			}
		}
	}
	return nil, fmt.Errorf("Could not find that table: %v", tableName)
}

func (m *Schema) AddTableName(tableName string, ss *SourceSchema) {
	found := false
	for _, curTableName := range m.tableNames {
		if tableName == curTableName {
			found = true
		}
	}
	if !found {
		m.tableNames = append(m.tableNames, tableName)
		sort.Strings(m.tableNames)
		if _, ok := m.tableMap[tableName]; !ok {
			m.tableSources[tableName] = ss
			m.tableMap[tableName] = nil
		}
	}
}
func (m *Schema) addTable(tbl *Table) {
	m.tableSources[tbl.Name] = tbl.SourceSchema
	m.tableMap[tbl.Name] = tbl
	m.AddTableName(tbl.Name, tbl.SourceSchema)
}

// Is this schema object within time window described by @dur time ago ?
func (m *Schema) Since(dur time.Duration) bool {
	if m.lastRefreshed.IsZero() {
		return false
	}
	if m.lastRefreshed.After(time.Now().Add(dur)) {
		return true
	}
	return false
}

func NewSourceSchema(name, sourceType string) *SourceSchema {
	m := &SourceSchema{
		Name:       name,
		Conf:       NewSourceConfig(name, sourceType),
		Nodes:      make([]*NodeConfig, 0),
		tableNames: make([]string, 0),
		tableMap:   make(map[string]*Table),
	}
	return m
}
func (m *SourceSchema) AddTableName(tableName string) {

	// check if we only want to load certain tables from this source
	lowerTable := tableName
	if len(m.Conf.TablesToLoad) > 0 {
		loadTable := false
		for _, tblToLoad := range m.Conf.TablesToLoad {
			if strings.ToLower(tblToLoad) == lowerTable {
				loadTable = true
				break
			}
		}
		if !loadTable {
			return
		}
	}

	// see if we already have this table
	found := false
	for _, curTableName := range m.tableNames {
		if tableName == curTableName {
			found = true
		}
	}
	if !found {
		m.tableNames = append(m.tableNames, tableName)
		sort.Strings(m.tableNames)
		if m.Schema == nil {
			//u.LogTracef(u.WARN, "%p WAT?  nil schema?  %#v", m, m)
			//u.Warnf("%p SourceSchema no schema ", m)
		} else {
			m.Schema.AddTableName(tableName, m)
		}
		if _, ok := m.tableMap[tableName]; !ok {
			m.tableMap[tableName] = nil
		}
	}
}
func (m *SourceSchema) AddTable(tbl *Table) {
	hash := fnv.New64()
	if m.Schema != nil {
		// Do id's need to be unique across schemas?   seems bit overkill
		//hash.Write([]byte(m.Name + tbl.Name))
		hash.Write([]byte(tbl.Name))
		m.Schema.addTable(tbl)
	} else {
		hash.Write([]byte(tbl.Name))
		//u.Warnf("no SCHEMA!!!!!! %#v", tbl)
	}
	// create consistent-hash-id of this table name, and or table+schema
	tbl.tblId = hash.Sum64()
	m.tableMap[tbl.Name] = tbl
	m.AddTableName(tbl.Name)
}
func (m *SourceSchema) Tables() []string { return m.tableNames }
func (m *SourceSchema) Table(tableName string) (*Table, error) {
	tbl, ok := m.tableMap[tableName]
	if ok && tbl != nil {
		return tbl, nil
	} else if ok && tbl == nil {
		//u.Infof("try to get table from source schema %v", tableName)
		if sourceTable, ok := m.DS.(SchemaProvider); ok {
			tbl, err := sourceTable.Table(tableName)
			if err == nil {
				m.AddTable(tbl)
			}
			return tbl, err
		}
	}
	if tbl != nil && !tbl.Current() {
		// What?
		if sourceTable, ok := m.DS.(SchemaProvider); ok {
			tbl, err := sourceTable.Table(tableName)
			if err == nil {
				m.AddTable(tbl)
			}
			return tbl, err
		}
	}
	return nil, fmt.Errorf("Could not find that table: %v", tableName)
}

func NewTable(table string, s *SourceSchema) *Table {
	t := &Table{
		Name:         strings.ToLower(table),
		NameOriginal: table,
		Fields:       make([]*Field, 0),
		FieldMap:     make(map[string]*Field),
		SourceSchema: s,
	}
	t.SetRefreshed()
	t.init()
	return t
}

func (m *Table) init() {}

func (m *Table) HasField(name string) bool {
	if _, ok := m.FieldMap[name]; ok {
		return true
	}
	return false
}
func (m *Table) FieldsAsMessages() []Message {
	msgs := make([]Message, len(m.Fields))
	for i, f := range m.Fields {
		msgs[i] = f
	}
	return msgs
}
func (m *Table) Id() uint64        { return m.tblId }
func (m *Table) Body() interface{} { return m }

// func (m *Table) DescribeColumn(values []driver.Value) {
// 	m.DescribeValues = append(m.DescribeValues, values)
// }

func (m *Table) AddField(fld *Field) {
	found := false
	for i, curFld := range m.Fields {
		if curFld.Name == fld.Name {
			found = true
			m.Fields[i] = fld
			break
		}
	}
	if !found {
		fld.idx = uint64(len(m.Fields))
		m.Fields = append(m.Fields, fld)
	}
	m.FieldMap[fld.Name] = fld
}

func (m *Table) AddFieldType(name string, valType value.ValueType) {
	m.AddField(&Field{Type: valType, Name: name})
}

// Explicityly set column names
func (m *Table) SetColumns(cols []string) {
	//u.LogTracef(u.WARN, "who uses me?")
	m.cols = cols
	m.FieldPositions = make(map[string]int, len(cols))
	for idx, col := range cols {
		m.FieldPositions[col] = idx
	}
}

func (m *Table) Columns() []string { return m.cols }

// List of Field Names and ordinal position in Column list
func (m *Table) FieldNamesPositions() map[string]int { return m.FieldPositions }

// Is this schema object current?  ie, have we refreshed it from
//  source since refresh interval
func (m *Table) Current() bool { return m.Since(SchemaRefreshInterval) }

// update the refreshed date to now
func (m *Table) SetRefreshed() { m.lastRefreshed = time.Now() }

// Is this schema object within time window described by @dur time ago ?
func (m *Table) Since(dur time.Duration) bool {
	u.Debugf("table?  %+v", m)
	if m.lastRefreshed.IsZero() {
		return false
	}
	if m.lastRefreshed.After(time.Now().Add(dur)) {
		return true
	}
	return false
}

func NewFieldBase(name string, valType value.ValueType, size int, description string) *Field {
	return &Field{
		Name:        name,
		Description: description,
		Length:      uint32(size),
		Type:        valType,
	}
}
func NewField(name string, valType value.ValueType, size int, allowNulls bool, defaultVal driver.Value, key, collation, description string) *Field {
	return &Field{
		Name:         name,
		Description:  description,
		Collation:    collation,
		Length:       uint32(size),
		Type:         valType,
		NoNulls:      !allowNulls,
		DefaultValue: defaultVal,
		Key:          key,
	}
}

func (m *Field) Id() uint64        { return m.idx }
func (m *Field) Body() interface{} { return m }

func NewDescribeFullHeaders() []*Field {
	fields := make([]*Field, 9)
	//[]string{"Field", "Type", "Collation", "Null", "Key", "Default", "Extra", "Privileges", "Comment"}
	fields[0] = NewFieldBase("Field", value.StringType, 255, "COLUMN_NAME")
	fields[1] = NewFieldBase("Type", value.StringType, 32, "COLUMN_TYPE")
	fields[2] = NewFieldBase("Collation", value.StringType, 32, "COLUMN_COLLATION")
	fields[3] = NewFieldBase("Null", value.StringType, 4, "IS_NULLABLE")
	fields[4] = NewFieldBase("Key", value.StringType, 64, "COLUMN_KEY")
	fields[5] = NewFieldBase("Default", value.StringType, 32, "COLUMN_DEFAULT")
	fields[6] = NewFieldBase("Extra", value.StringType, 255, "")
	fields[7] = NewFieldBase("Privileges", value.StringType, 255, "")
	fields[8] = NewFieldBase("Comment", value.StringType, 255, "")
	return fields
}
func NewDescribeHeaders() []*Field {
	fields := make([]*Field, 6)
	//[]string{"Field", "Type",  "Null", "Key", "Default", "Extra"}
	fields[0] = NewFieldBase("Field", value.StringType, 255, "COLUMN_NAME")
	fields[1] = NewFieldBase("Type", value.StringType, 32, "COLUMN_TYPE")
	fields[2] = NewFieldBase("Null", value.StringType, 4, "IS_NULLABLE")
	fields[3] = NewFieldBase("Key", value.StringType, 64, "COLUMN_KEY")
	fields[4] = NewFieldBase("Default", value.StringType, 32, "COLUMN_DEFAULT")
	fields[5] = NewFieldBase("Extra", value.StringType, 255, "")
	return fields
}

func NewSourceConfig(name, sourceType string) *SourceConfig {
	return &SourceConfig{
		Name:       name,
		SourceType: sourceType,
	}
}

func (m *SourceConfig) Init() {
	// if len(m.TablesToLoad) > 0 && len(m.tablesLoadMap) == 0 {
	// 	tm := make(map[string]struct{})
	// 	for _, tbl := range m.TablesToLoad {
	// 		tm[tbl] = struct{}{}
	// 	}
	// 	m.tablesLoadMap = tm
	// }
}

func (m *SourceConfig) String() string {
	return fmt.Sprintf(`<sourceconfig name=%q type=%q />`, m.Name, m.SourceType)
}