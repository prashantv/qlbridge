package expr

import (
	"math"

	u "github.com/araddon/gou"
	"github.com/araddon/qlbridge/value"
)

var _ = u.EMPTY

const yymmTimeLayout = "0601"

func init() {
	// agregate ops
	FuncAdd("count", CountFunc)

	// math
	FuncAdd("sqrt", SqrtFunc)
	FuncAdd("pow", PowFunc)
}

// Count
func CountFunc(ctx EvalContext, val value.Value) (value.IntValue, bool, error) {
	if val.Err() || val.Nil() {
		return value.NewIntValue(0), false, nil
	}
	//u.Infof("???   vals=[%v]", val.Value())
	return value.NewIntValue(1), true, nil
}

// Sqrt
func SqrtFunc(ctx EvalContext, val value.Value) (value.NumberValue, bool, error) {
	//func Sqrt(x float64) float64
	nv, ok := val.(value.NumericValue)
	if !ok {
		return value.NewNumberValue(math.NaN()), false, nil
	}
	if val.Err() || val.Nil() {
		return value.NewNumberValue(0), false, nil
	}
	fv := nv.Float()
	fv = math.Sqrt(fv)
	//u.Infof("???   vals=[%v]", val.Value())
	return value.NewNumberValue(fv), true, nil
}

// Pow
func PowFunc(ctx EvalContext, val, toPower value.Value) (value.NumberValue, bool, error) {
	//Pow(x, y float64) float64
	//u.Infof("powFunc:  %T:%v %T:%v ", val, val.Value(), toPower, toPower.Value())
	if val.Err() || val.Nil() {
		return value.NewNumberValue(0), false, nil
	}
	if toPower.Err() || toPower.Nil() {
		return value.NewNumberValue(0), false, nil
	}
	fv, _ := value.ToFloat64(val.Rv())
	pow, _ := value.ToFloat64(toPower.Rv())
	if math.IsNaN(fv) || math.IsNaN(pow) {
		return value.NewNumberValue(0), false, nil
	}
	fv = math.Pow(fv, pow)
	//u.Infof("pow ???   vals=[%v]", fv, pow)
	return value.NewNumberValue(fv), true, nil
}
