// Mockscsvtestdata is csv test data only used for tests.
package mockcsvtestdata

import (
	"sync"

	"github.com/araddon/qlbridge/datasource"
	"github.com/araddon/qlbridge/datasource/mockcsv"
	"github.com/araddon/qlbridge/expr/builtins"
	"github.com/araddon/qlbridge/plan"
	"github.com/araddon/qlbridge/schema"
)

var (
	loadData   sync.Once
	MockSchema *schema.Schema
)

func TestContext(query string) *plan.Context {
	ctx := plan.NewContext(query)
	ctx.DisableRecover = true
	ctx.Schema = MockSchema
	ctx.Session = datasource.NewMySqlSessionVars()
	return ctx
}

func SchemaLoader(name string) (*schema.Schema, error) {
	return MockSchema, nil
}

func LoadTestDataOnce() {
	loadData.Do(func() {

		MockSchema, _ = datasource.DataSourcesRegistry().Schema(mockcsv.MockSchemaName)
		if MockSchema == nil {
			panic("MockSchema Must Exist")
		}

		// Load in a "csv file" into our mock data store
		mockcsv.LoadTable(mockcsv.MockSchemaName, "users", `user_id,email,interests,reg_date,referral_count
9Ip1aKbeZe2njCDM,"aaron@email.com","fishing","2012-10-17T17:29:39.738Z",82
hT2impsOPUREcVPc,"bob@email.com","swimming","2009-12-11T19:53:31.547Z",12
hT2impsabc345c,"not_an_email_2",,"2009-12-11T19:53:31.547Z",12`)

		mockcsv.LoadTable(mockcsv.MockSchemaName, "orders", `order_id,user_id,item_id,price,order_date,item_count
1,9Ip1aKbeZe2njCDM,1,22.50,"2012-12-24T17:29:39.738Z",82
2,9Ip1aKbeZe2njCDM,2,37.50,"2013-10-24T17:29:39.738Z",82
3,abcabcabc,1,22.50,"2013-10-24T17:29:39.738Z",82
`)

		builtins.LoadAllBuiltins()

	})
}
