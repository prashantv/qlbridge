package plan

import (
	"fmt"
	"strings"

	u "github.com/araddon/gou"

	"github.com/araddon/qlbridge/expr"
	"github.com/araddon/qlbridge/rel"
)

var _ = u.EMPTY

// Rewrite Schema SHOW Statements AS SELECT statements
//  so we only need a Select Planner, not separate planner for show statements
func RewriteShowAsSelect(stmt *rel.SqlShow, ctx *Context) (*rel.SqlSelect, error) {

	raw := strings.ToLower(stmt.Raw)

	showType := strings.ToLower(stmt.ShowType)
	u.Debugf("showType=%q from=%q rewrite: %s", showType, stmt.From, raw)
	sqlStatement := ""
	switch showType {
	case "tables":
		from := "tables"
		if stmt.Db != "" {
			from = fmt.Sprintf("%s.%s", stmt.Db, expr.IdentityMaybeQuote('`', from))
		}
		if stmt.Full {
			// SHOW FULL TABLES;    = select name, table_type from tables;
			// TODO:  note the stupid "_in_mysql", assuming i don't have to implement
			/*
			   mysql> show full tables;
			   +---------------------------+------------+
			   | Tables_in_mysql           | Table_type |
			   +---------------------------+------------+
			   | columns_priv              | BASE TABLE |

			*/
			sqlStatement = fmt.Sprintf("select Table, Table_Type from %s;", from)

		} else {
			// show tables;
			sqlStatement = fmt.Sprintf("select Table from %s;", from)
		}
		//case stmt.Create && strings.ToLower(stmt.CreateWhat) == "table":
		// SHOW CREATE TABLE
	case "databases":
		// SHOW databases;  ->  select Database from databases;
		sqlStatement = "select Database from databases;"
	case "columns":
		if stmt.Full {
			/*
				mysql> show full columns from user;
				+------------------------+-----------------------------------+-----------------+------+-----+-----------------------+-------+---------------------------------+---------+
				| Field                  | Type                              | Collation       | Null | Key | Default               | Extra | Privileges                      | Comment |

			*/
			sqlStatement = fmt.Sprintf("select Field, Type, Collation, `Null`, Key, Default, Extra, Privileges, Comment from `schema`.`%s`;", stmt.Identity)

		} else {
			/*
				mysql> show columns from user;
				+------------------------+-----------------------------------+------+-----+-----------------------+-------+
				| Field                  | Type                              | Null | Key | Default               | Extra |
				+------------------------+-----------------------------------+------+-----+-----------------------+-------+
			*/
			sqlStatement = fmt.Sprintf("select Field, Type, `Null`, Key, Default, Extra from `schema`.`%s`;", stmt.Identity)
		}
	case "keys", "indexes", "index":
		/*
			mysql> show keys from user;
			+-------+------------+----------+--------------+-------------+-----------+-------------+----------+--------+------+------------+---------+---------------+
			| Table | Non_unique | Key_name | Seq_in_index | Column_name | Collation | Cardinality | Sub_part | Packed | Null | Index_type | Comment | Index_comment |
			+-------+------------+----------+--------------+-------------+-----------+-------------+----------+--------+------+------------+---------+---------------+
			| user  |          0 | PRIMARY  |            1 | Host        | A         |        NULL |     NULL | NULL   |      | BTREE      |         |               |
			| user  |          0 | PRIMARY  |            2 | User        | A         |           7 |     NULL | NULL   |      | BTREE      |         |               |
			+-------+------------+----------+--------------+-------------+-----------+-------------+----------+--------+------+------------+---------+---------------+
		*/
		sqlStatement = fmt.Sprintf("select Table, Non_unique, Key_name, Seq_in_index, Column_name, Collation, Cardinality, Sub_part, Packed, `Null`, Index_type, Index_comment from `schema`.`%s`;", stmt.Identity)

	//case "variables":
	// SHOW [GLOBAL | SESSION] VARIABLES [like_or_where]
	default:
		u.Warnf("unhandled %s", raw)
		return nil, fmt.Errorf("Unrecognized:   %s", raw)
	}
	sel, err := rel.ParseSqlSelect(sqlStatement)
	if err != nil {
		return nil, err
	}
	sel.SetSystemQry()
	if stmt.Like != nil {
		//u.Debugf("like? %v", stmt.Like)
		sel.Where = &rel.SqlWhere{Expr: stmt.Like}
	} else if stmt.Where != nil {
		//u.Debugf("add where: %s", stmt.Where)
		sel.Where = &rel.SqlWhere{Expr: stmt.Where}
	}
	if ctx.Schema == nil {
		u.Warnf("missing schema")
		return nil, fmt.Errorf("Must have schema")
	}

	ctx.Schema = ctx.Schema.InfoSchema
	if ctx.Schema == nil {
		u.Warnf("WAT?  Still nil info schema?")
	}
	u.Debugf("schema rewrite: %q  ==> %s", stmt.Raw, sel.String())
	return sel, nil
}
func RewriteDescribeAsSelect(stmt *rel.SqlDescribe, ctx *Context) (*rel.SqlSelect, error) {
	s := &rel.SqlShow{ShowType: "columns", Identity: stmt.Identity, Raw: stmt.Raw}
	return RewriteShowAsSelect(s, ctx)
}
