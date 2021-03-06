package expr_test

import (
	"testing"

	u "github.com/araddon/gou"
	"github.com/bmizerany/assert"
	"github.com/gogo/protobuf/proto"

	"github.com/araddon/qlbridge/expr"
)

var pbTests = []string{
	`eq(event,"stuff") OR ge(party, 1)`,
	`"Portland" IN ("ohio")`,
	`"xyz" BETWEEN todate("1/1/2015") AND 50`,
	`name == "bob"`,
	`name = 'bob'`,
}

func TestNodePb(t *testing.T) {
	t.Parallel()
	for _, exprText := range pbTests {
		et, err := expr.ParseExpression(exprText)
		assert.T(t, err == nil, "Should not error parse expr but got ", err, "for ", exprText)
		pb := et.Root.ToPB()
		assert.Tf(t, pb != nil, "was nil PB: %#v", et.Root)
		pbBytes, err := proto.Marshal(pb)
		assert.Tf(t, err == nil, "Should not error on proto.Marshal but got [%v] for %s pb:%#v", err, exprText, pb)
		n2, err := expr.NodeFromPb(pbBytes)
		assert.T(t, err == nil, "Should not error from pb but got ", err, "for ", exprText)
		assert.T(t, et.Root.Equal(n2), "Equal?")
		u.Infof("pre/post: \n\t%s\n\t%s", et.Root, n2)
	}
}

var _ = u.EMPTY
