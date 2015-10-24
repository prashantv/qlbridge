package vm

import (
	"encoding/json"
	"fmt"
	"math"
	"reflect"
	"runtime"
	"time"

	u "github.com/araddon/gou"
	"github.com/araddon/qlbridge/expr"
	"github.com/araddon/qlbridge/lex"
	"github.com/araddon/qlbridge/value"
	"github.com/mb0/glob"
)

var (
	ErrUnknownOp       = fmt.Errorf("expr: unknown op type")
	ErrUnknownNodeType = fmt.Errorf("expr: unknown node type")
	ErrExecute         = fmt.Errorf("Could not execute")
	_                  = u.EMPTY

	SchemaInfoEmpty = &NoSchema{}

	// our DataTypes we support, a limited sub-set of go
	floatRv   = reflect.ValueOf(float64(1.2))
	int64Rv   = reflect.ValueOf(int64(1))
	int32Rv   = reflect.ValueOf(int32(1))
	stringRv  = reflect.ValueOf("")
	stringsRv = reflect.ValueOf([]string{""})
	boolRv    = reflect.ValueOf(true)
	mapIntRv  = reflect.ValueOf(map[string]int64{"hi": int64(1)})
	timeRv    = reflect.ValueOf(time.Time{})
	nilRv     = reflect.ValueOf(nil)
)

type State struct {
	ExprVm // reference to the VM operating on this state
	// We make a reflect value of self (state) as we use []reflect.ValueOf often
	rv reflect.Value
	expr.ContextReader
	Writer expr.ContextWriter
}

func NewState(vm ExprVm, read expr.ContextReader, write expr.ContextWriter) *State {
	s := &State{
		ExprVm:        vm,
		ContextReader: read,
		Writer:        write,
	}
	s.rv = reflect.ValueOf(s)
	return s
}

type EvalBaseContext struct {
	expr.ContextReader
}
type EvaluatorFunc func(ctx expr.EvalContext) (value.Value, error)

type ExprVm interface {
	Execute(writeContext expr.ContextWriter, readContext expr.ContextReader) error
}

type NoSchema struct {
}

func (m *NoSchema) Key() string { return "" }

// A node vm is a vm for parsing, evaluating a single tree-node
//
type Vm struct {
	*expr.Tree
}

func (m *Vm) MarshalJSON() ([]byte, error) {
	return json.Marshal(m.String())
}

func NewVm(exprText string) (*Vm, error) {
	t, err := expr.ParseExpression(exprText)
	if err != nil {
		return nil, err
	}
	m := &Vm{
		Tree: t,
	}
	return m, nil
}

// Execute applies a parse expression to the specified context's
func (m *Vm) Execute(writeContext expr.ContextWriter, readContext expr.ContextReader) (err error) {
	defer errRecover(&err)
	s := &State{
		ExprVm:        m,
		ContextReader: readContext,
	}
	s.rv = reflect.ValueOf(s)
	//u.Debugf("vm.Execute:  %#v", m.Tree.Root)
	v, err := s.Walk(m.Tree.Root)

	if err != nil {
		return err
	}

	// vm returned an error value
	if errv, ok := v.(value.ErrorValue); ok {
		return errv
	}

	// Special Vm that doesnt' have named fields, single tree expression
	//u.Debugf("vm.Walk val:  %v", v)
	writeContext.Put(SchemaInfoEmpty, readContext, v)
	return nil
}

// errRecover is the handler that turns panics into returns from the top
// level of
func errRecover(errp *error) {
	e := recover()
	if e != nil {
		switch err := e.(type) {
		case runtime.Error:
			panic(e)
		case error:
			*errp = err
		default:
			panic(e)
		}
	}
}

// creates a new Value with a nil group and given value.
// TODO:  convert this to an interface method on nodes called Value()
func numberNodeToValue(t *expr.NumberNode) (value.Value, error) {
	//u.Debugf("nodeToValue()  isFloat?%v", t.IsFloat)
	var v value.Value
	if t.IsInt {
		v = value.NewIntValue(t.Int64)
	} else if t.IsFloat {
		fv, ok := value.ToFloat64(reflect.ValueOf(t.Text))
		if !ok {
			u.Warnf("Could not perform numeric conversion for %q", t.Text)
			return value.NilValueVal, fmt.Errorf("Could not convert to number %v", t.Text)
		}
		v = value.NewNumberValue(fv)
	} else {
		u.Warnf("Could not find numeric conversion for %v", t.Type())
		return value.NilValueVal, nil
	}
	//u.Debugf("return nodeToValue()	%v  %T  arg:%T", v, v, t)
	return v, nil
}

func Evaluator(arg expr.Node) EvaluatorFunc {
	//u.Debugf("Evaluator() node=%T  %v", arg, arg)
	switch argVal := arg.(type) {
	case *expr.NumberNode:
		return func(ctx expr.EvalContext) (value.Value, error) { return numberNodeToValue(argVal) }
	case *expr.BinaryNode:
		return func(ctx expr.EvalContext) (value.Value, error) { return walkBinary(ctx, argVal) }
	case *expr.UnaryNode:
		return func(ctx expr.EvalContext) (value.Value, error) { return walkUnary(ctx, argVal) }
	case *expr.FuncNode:
		return func(ctx expr.EvalContext) (value.Value, error) { return walkFunc(ctx, argVal) }
	case *expr.IdentityNode:
		return func(ctx expr.EvalContext) (value.Value, error) { return walkIdentity(ctx, argVal) }
	case *expr.StringNode:
		return func(ctx expr.EvalContext) (value.Value, error) { return value.NewStringValue(argVal.Text), nil }
	case *expr.TriNode:
		return func(ctx expr.EvalContext) (value.Value, error) { return walkTri(ctx, argVal) }
	case *expr.MultiArgNode:
		return func(ctx expr.EvalContext) (value.Value, error) { return walkMulti(ctx, argVal) }
	default:
		return func(ctx expr.EvalContext) (value.Value, error) {
			return nil, fmt.Errorf("Unknown Node Type %T", argVal)
		}
	}
}

func Eval(ctx expr.EvalContext, arg expr.Node) (value.Value, error) {
	//u.Debugf("Eval() node=%T  %v", arg, arg)
	// can we switch to arg.Type()
	switch argVal := arg.(type) {
	case *expr.NumberNode:
		return numberNodeToValue(argVal)
	case *expr.BinaryNode:
		return walkBinary(ctx, argVal)
	case *expr.UnaryNode:
		return walkUnary(ctx, argVal)
	case *expr.TriNode:
		return walkTri(ctx, argVal)
	case *expr.MultiArgNode:
		return walkMulti(ctx, argVal)
	case *expr.FuncNode:
		return walkFunc(ctx, argVal)
	case *expr.IdentityNode:
		return walkIdentity(ctx, argVal)
	case *expr.StringNode:
		return value.NewStringValue(argVal.Text), nil
	case nil:
		return nil, nil
	default:
		return nil, fmt.Errorf("Unknown Node Type %T", argVal)
	}
}

func (e *State) Walk(arg expr.Node) (value.Value, error) {
	return Eval(e.ContextReader, arg)
}

func walkBinary(ctx expr.EvalContext, node *expr.BinaryNode) (value.Value, error) {
	ar, aerr := Eval(ctx, node.Args[0])
	br, berr := Eval(ctx, node.Args[1])
	if aerr != nil || berr != nil {
		// If !aok, but token is a Negate?
		u.Warnf("walkBinary not ok: op=%s %v  l:%v  r:%v  %T  %T", node.Operator, node, ar, br, ar, br)
		//u.Debugf("node: %s   --- %s", node.Args[0], node.Args[1])
		// if ar != nil && br != nil {
		// 	u.Debugf("not ok: %v  l:%v  r:%v", node, ar.ToString(), br.ToString())
		// }
		return nil, fmt.Errorf("aerr:%v berr:%v", aerr, berr)
	}
	// if ar == nil {
	// 	u.Warnf("Wat? %q node0: %#v", node.Args[0], node.Args[0])
	// 	//return nil, false
	// }
	// if br == nil {
	// 	u.Warnf("wat2  %q node1: %#v", node.Args[1], node.Args[1])
	// 	//return nil, false
	// }
	//u.Debugf("node.Args: %#v", node.Args)
	//u.Debugf("walkBinary: %v  l:%v  r:%v  %T  %T", node, ar, br, ar, br)
	switch at := ar.(type) {
	case value.IntValue:
		switch bt := br.(type) {
		case value.IntValue:
			//u.Debugf("doing operate ints  %v %v  %v", at, node.Operator.V, bt)
			n := operateInts(node.Operator, at, bt)
			return n, nil
		case value.NumberValue:
			//u.Debugf("doing operate ints/numbers  %v %v  %v", at, node.Operator.V, bt)
			n := operateNumbers(node.Operator, at.NumberValue(), bt)
			return n, nil
		default:
			u.Errorf("unknown type:  %T %v", bt, bt)
		}
	case value.NumberValue:
		switch bt := br.(type) {
		case value.IntValue:
			n := operateNumbers(node.Operator, at, bt.NumberValue())
			return n, nil
		case value.NumberValue:
			n := operateNumbers(node.Operator, at, bt)
			return n, nil
		default:
			u.Errorf("unknown type:  %T %v", bt, bt)
		}
	case value.BoolValue:
		switch bt := br.(type) {
		case value.BoolValue:
			atv, btv := at.Value().(bool), bt.Value().(bool)
			switch node.Operator.T {
			case lex.TokenLogicAnd:
				return value.NewBoolValue(atv && btv), nil
			case lex.TokenLogicOr, lex.TokenOr:
				return value.NewBoolValue(atv || btv), nil
			case lex.TokenEqualEqual, lex.TokenEqual:
				return value.NewBoolValue(atv == btv), nil
			case lex.TokenNE:
				return value.NewBoolValue(atv != btv), nil
			default:
				u.Warnf("bool binary?:  %#v  %v %v", node, at, bt)
			}
		case nil, value.NilValue:
			switch node.Operator.T {
			case lex.TokenLogicAnd:
				return value.NewBoolValue(false), nil
			case lex.TokenLogicOr, lex.TokenOr:
				return at, nil
			case lex.TokenEqualEqual, lex.TokenEqual:
				return value.NewBoolValue(false), nil
			case lex.TokenNE:
				return value.NewBoolValue(true), nil
			// case lex.TokenGE, lex.TokenGT, lex.TokenLE, lex.TokenLT:
			// 	return value.NewBoolValue(false), true
			default:
				u.Warnf("right side nil binary:  %q", node)
				return nil, nil
			}
		default:
			u.Warnf("br: %#v", br)
			u.Errorf("at?%T  %v  coerce?%v bt? %T     %v", at, at.Value(), at.CanCoerce(stringRv), bt, bt.Value())
		}
	case value.StringValue:
		switch bt := br.(type) {
		case value.StringValue:
			// Nice, both strings
			return operateStrings(node.Operator, at, bt), nil
		case nil, value.NilValue:
			switch node.Operator.T {
			case lex.TokenEqualEqual, lex.TokenEqual:
				if at.Nil() {
					return value.NewBoolValue(true), nil
				}
				return value.NewBoolValue(false), nil
			case lex.TokenNE:
				if at.Nil() {
					return value.NewBoolValue(false), nil
				}
				return value.NewBoolValue(true), nil
			default:
				u.Warnf("unsupported op: %v", node.Operator)
				return nil, fmt.Errorf("unsupported op: %v", node.Operator)
			}
		case value.BoolValue:
			if value.IsBool(at.Val()) {
				//u.Warnf("bool eval:  %v %v %v  :: %v", value.BoolStringVal(at.Val()), node.Operator.T.String(), bt.Val(), value.NewBoolValue(value.BoolStringVal(at.Val()) == bt.Val()))
				switch node.Operator.T {
				case lex.TokenEqualEqual, lex.TokenEqual:
					return value.NewBoolValue(value.BoolStringVal(at.Val()) == bt.Val()), nil
				case lex.TokenNE:
					return value.NewBoolValue(value.BoolStringVal(at.Val()) != bt.Val()), nil
				default:
					u.Warnf("unsupported op: %v", node.Operator)
					return nil, fmt.Errorf("unsupported op: %v", node.Operator)
				}
			} else {
				// Should we evaluate strings that are non-nil to be = true?
				u.Debugf("not handled: boolean %v %T=%v  expr: %s", node.Operator, at.Value(), at.Val(), node.String())
				return nil, fmt.Errorf("unhandled boolean: %v   %#v", node.Operator, at)
			}
		default:
			// TODO:  this doesn't make sense, we should be able to operate on other types
			if at.CanCoerce(int64Rv) {
				switch bt := br.(type) {
				case value.StringValue:
					n := operateNumbers(node.Operator, at.NumberValue(), bt.NumberValue())
					return n, nil
				case value.IntValue:
					n := operateNumbers(node.Operator, at.NumberValue(), bt.NumberValue())
					return n, nil
				case value.NumberValue:
					n := operateNumbers(node.Operator, at.NumberValue(), bt)
					return n, nil
				default:
					u.Errorf("at?%T  %v  coerce?%v bt? %T     %v", at, at.Value(), at.CanCoerce(stringRv), bt, bt.Value())
				}
			} else {
				u.Errorf("at?%T  %v  coerce?%v bt? %T     %v", at, at.Value(), at.CanCoerce(stringRv), br, br)
			}
		}
	case nil, value.NilValue:
		switch node.Operator.T {
		case lex.TokenLogicAnd:
			return value.NewBoolValue(false), nil
		case lex.TokenLogicOr, lex.TokenOr:
			switch bt := br.(type) {
			case value.BoolValue:
				return bt, nil
			default:
				return value.NewBoolValue(false), nil
			}
		case lex.TokenEqualEqual, lex.TokenEqual:
			// does nil==nil  = true ??
			switch br.(type) {
			case nil, value.NilValue:
				return value.NewBoolValue(true), nil
			default:
				return value.NewBoolValue(false), nil
			}
		case lex.TokenNE:
			return value.NewBoolValue(true), nil
		// case lex.TokenGE, lex.TokenGT, lex.TokenLE, lex.TokenLT:
		// 	return value.NewBoolValue(false), true
		default:
			u.Debugf("left side nil binary:  %q", node)
			return nil, nil
		}
	default:
		u.Debugf("Unknown op?  %T  %T  %v", ar, at, ar)
		return nil, fmt.Errorf("unsupported left side value: %T in %s", at, node)
	}

	return nil, fmt.Errorf("unsupported binary expression: %s", node)
}

func walkIdentity(ctx expr.EvalContext, node *expr.IdentityNode) (value.Value, error) {

	if node.IsBooleanIdentity() {
		//u.Debugf("walkIdentity() boolean: node=%T  %v Bool:%v", node, node, node.Bool())
		return value.NewBoolValue(node.Bool()), nil
	}
	if ctx == nil {
		return value.NewStringValue(node.Text), nil
	}
	//u.Debugf("walkIdentity() node=%T  %v", node, node)
	return ctx.Get(node.Text)
}

func walkUnary(ctx expr.EvalContext, node *expr.UnaryNode) (value.Value, error) {

	a, ok := Eval(ctx, node.Arg)
	if !ok {
		if node.Operator.T == lex.TokenExists {
			return value.NewBoolValue(false), nil
		}
		u.Debugf("unary could not evaluate %#v", node)
		return a, false
	}
	switch node.Operator.T {
	case lex.TokenNegate:
		switch argVal := a.(type) {
		case value.BoolValue:
			//u.Infof("found unary bool:  res=%v   expr=%v", !argVal.v, node.StringAST())
			return value.NewBoolValue(!argVal.Val()), true
		default:
			u.Errorf("unary type not implemented. Unknonwn node type: %T:%v", argVal, argVal)
			panic(ErrUnknownNodeType)
		}
	case lex.TokenMinus:
		if an, aok := a.(value.NumericValue); aok {
			return value.NewNumberValue(-an.Float()), true
		}
	case lex.TokenExists:
		switch a.(type) {
		case nil:
			return value.NewBoolValue(false), true
		case value.NilValue:
			return value.NewBoolValue(false), true
		}
		return value.NewBoolValue(true), true
	default:
		u.Warnf("urnary not implemented for type %s %#v", node.Operator.T.String(), node)
	}

	return value.NewNilValue(), false
}

// TriNode evaluator
//
//     A   BETWEEN   B  AND C
//
func walkTri(ctx expr.EvalContext, node *expr.TriNode) (value.Value, error) {

	a, aok := Eval(ctx, node.Args[0])
	b, bok := Eval(ctx, node.Args[1])
	c, cok := Eval(ctx, node.Args[2])
	//u.Infof("tri:  %T:%v  %v  %T:%v   %T:%v", a, a, node.Operator, b, b, c, c)
	if !aok || !bok || !cok {
		u.Debugf("Could not evaluate args, %#v", node.String())
		return value.BoolValueFalse, false
	}
	switch node.Operator.T {
	case lex.TokenBetween:
		switch a.Type() {
		case value.IntType:
			//u.Infof("found tri:  %v %v %v  expr=%v", a, b, c, node.StringAST())
			if aiv, ok := a.(value.IntValue); ok {
				if biv, ok := b.(value.IntValue); ok {
					if civ, ok := c.(value.IntValue); ok {
						if aiv.Int() > biv.Int() && aiv.Int() < civ.Int() {
							return value.NewBoolValue(true), true
						} else {
							return value.NewBoolValue(false), true
						}
					}
				}
			}
			return value.BoolValueFalse, false
		case value.NumberType:
			//u.Infof("found tri:  %v %v %v  expr=%v", a, b, c, node.StringAST())
			if afv, ok := a.(value.NumberValue); ok {
				if bfv, ok := b.(value.NumberValue); ok {
					if cfv, ok := c.(value.NumberValue); ok {
						if afv.Float() > bfv.Float() && afv.Float() < cfv.Float() {
							return value.NewBoolValue(true), false
						} else {
							return value.NewBoolValue(false), true
						}
					}
				}
			}
			return value.BoolValueFalse, false
		default:
			u.Warnf("between not implemented for type %s %#v", a.Type().String(), node)
		}
	default:
		u.Warnf("tri node walk not implemented:   %#v", node)
	}

	return value.NewNilValue(), false
}

// MultiNode evaluator
//
//     A   IN   (b,c,d)
//
func walkMulti(ctx expr.EvalContext, node *expr.MultiArgNode) (value.Value, error) {

	a, aok := Eval(ctx, node.Args[0])
	//u.Debugf("multi:  %T:%v  %v", a, a, node.Operator)
	if !aok {
		// this is expected, most likely to missing data to operate on
		//u.Debugf("Could not evaluate args, %#v", node.Args[0])
		return value.BoolValueFalse, false
	}
	if node.Operator.T != lex.TokenIN {
		u.Warnf("walk multiarg not implemented for node type %#v", node)
		return value.NilValueVal, false
	}

	// Support `"literal" IN identity`
	if len(node.Args) == 2 && node.Args[1].NodeType() == expr.IdentityNodeType {
		ident := node.Args[1].(*expr.IdentityNode)
		mval, ok := walkIdentity(ctx, ident)
		if !ok {
			// Failed to lookup ident
			return value.BoolValueFalse, true
		}

		sval, ok := mval.(value.Slice)
		if !ok {
			u.Debugf("expected slice but received %T", mval)
			return value.BoolValueFalse, false
		}

		for _, val := range sval.SliceValue() {
			match, err := value.Equal(val, a)
			if err != nil {
				// Couldn't compare values
				u.Debugf("IN: couldn't compare %s and %s", val, a)
				continue
			}
			if match {
				return value.BoolValueTrue, true
			}
		}
		// No match, return false
		return value.BoolValueFalse, true
	}

	for i := 1; i < len(node.Args); i++ {
		v, ok := Eval(ctx, node.Args[i])
		if ok {
			//u.Debugf("in? %v %v", a, v)
			if eq, err := value.Equal(a, v); eq && err == nil {
				return value.NewBoolValue(true), true
			}
		} else {
			//u.Debugf("could not evaluate arg: %v", node.Args[i])
		}
	}
	return value.BoolValueFalse, true
}

func walkFunc(ctx expr.EvalContext, node *expr.FuncNode) (value.Value, error) {

	//u.Debugf("walkFunc node: %v", node.StringAST())

	// we create a set of arguments to pass to the function, first arg
	// is this Context
	var ok bool
	funcArgs := make([]reflect.Value, 0)
	if ctx != nil {
		funcArgs = append(funcArgs, reflect.ValueOf(ctx))
	} else {
		var nilArg expr.EvalContext
		funcArgs = append(funcArgs, reflect.ValueOf(&nilArg).Elem())
	}
	for _, a := range node.Args {

		//u.Debugf("arg %v  %T %v", a, a, a)

		var v interface{}

		switch t := a.(type) {
		case *expr.StringNode: // String Literal
			v = value.NewStringValue(t.Text)
		case *expr.IdentityNode: // Identity node = lookup in context

			if t.IsBooleanIdentity() {
				v = value.NewBoolValue(t.Bool())
			} else {
				v, ok = ctx.Get(t.Text)
				//u.Infof("%#v", ctx.Row())
				//u.Debugf("get '%s'? %T %v %v", t.String(), v, v, ok)
				if !ok {
					// nil arguments are valid
					v = value.NewNilValue()
				}
			}

		case *expr.NumberNode:
			v, ok = numberNodeToValue(t)
		case *expr.FuncNode:
			//u.Debugf("descending to %v()", t.Name)
			v, ok = walkFunc(ctx, t)
			if !ok {
				//return value.NewNilValue(), false
				// nil arguments are valid
				v = value.NewNilValue()
			}
			//u.Debugf("result of %v() = %v, %T", t.Name, v, v)
		case *expr.UnaryNode:
			v, ok = walkUnary(ctx, t)
			if !ok {
				// nil arguments are valid ??
				v = value.NewNilValue()
			}
		case *expr.BinaryNode:
			v, ok = walkBinary(ctx, t)
		case *expr.ValueNode:
			v = t.Value
		default:
			panic(fmt.Errorf("expr: unknown func arg type"))
		}

		if v == nil {
			//u.Warnf("Nil vals?  %v  %T  arg:%T", v, v, a)
			// What do we do with Nil Values?
			switch a.(type) {
			case *expr.StringNode: // String Literal
				u.Warnf("NOT IMPLEMENTED T:%T v:%v", a, a)
			case *expr.IdentityNode: // Identity node = lookup in context
				v = value.NewStringValue("")
			default:
				u.Warnf("un-handled type:  %v  %T", v, v)
			}

			funcArgs = append(funcArgs, reflect.ValueOf(v))
		} else {
			//u.Debugf(`found func arg:  "%v"  %T  arg:%T`, v, v, a)
			funcArgs = append(funcArgs, reflect.ValueOf(v))
		}

	}
	// Get the result of calling our Function (Value,bool)
	//u.Debugf("Calling func:%v(%v) %v", node.F.Name, funcArgs, node.F.F)
	fnRet := node.F.F.Call(funcArgs)
	//u.Debugf("fnRet: %v    ok?%v", fnRet, fnRet[1].Bool())
	// check if has an error response?
	if len(fnRet) > 1 && !fnRet[1].Bool() {
		// What do we do if not ok?
		return value.EmptyStringValue, false
	}
	//u.Debugf("response %v %v  %T", node.F.Name, fnRet[0].Interface(), fnRet[0].Interface())
	return fnRet[0].Interface().(value.Value), true
}

func operateNumbers(op lex.Token, av, bv value.NumberValue) value.Value {
	switch op.T {
	case lex.TokenPlus, lex.TokenStar, lex.TokenMultiply, lex.TokenDivide, lex.TokenMinus,
		lex.TokenModulus:
		if math.IsNaN(av.Val()) || math.IsNaN(bv.Val()) {
			return value.NewNumberValue(math.NaN())
		}
	}

	//
	a, b := av.Val(), bv.Val()
	switch op.T {
	case lex.TokenPlus: // +
		return value.NewNumberValue(a + b)
	case lex.TokenStar, lex.TokenMultiply: // *
		return value.NewNumberValue(a * b)
	case lex.TokenMinus: // -
		return value.NewNumberValue(a - b)
	case lex.TokenDivide: //    /
		return value.NewNumberValue(a / b)
	case lex.TokenModulus: //    %
		// is this even valid?   modulus on floats?
		return value.NewNumberValue(float64(int64(a) % int64(b)))

	// Below here are Boolean Returns
	case lex.TokenEqualEqual, lex.TokenEqual: //  ==
		//u.Infof("==?  %v  %v", av, bv)
		if a == b {
			return value.BoolValueTrue
		} else {
			return value.BoolValueFalse
		}
	case lex.TokenGT: //  >
		if a > b {
			//r = 1
			return value.BoolValueTrue
		} else {
			//r = 0
			return value.BoolValueFalse
		}
	case lex.TokenNE: //  !=    or <>
		if a != b {
			return value.BoolValueTrue
		} else {
			return value.BoolValueFalse
		}
	case lex.TokenLT: // <
		if a < b {
			return value.BoolValueTrue
		} else {
			return value.BoolValueFalse
		}
	case lex.TokenGE: // >=
		if a >= b {
			return value.BoolValueTrue
		} else {
			return value.BoolValueFalse
		}
	case lex.TokenLE: // <=
		if a <= b {
			return value.BoolValueTrue
		} else {
			return value.BoolValueFalse
		}
	case lex.TokenLogicOr, lex.TokenOr: //  ||
		if a != 0 || b != 0 {
			return value.BoolValueTrue
		} else {
			return value.BoolValueFalse
		}
	case lex.TokenLogicAnd: //  &&
		if a != 0 && b != 0 {
			return value.BoolValueTrue
		} else {
			return value.BoolValueFalse
		}
	}
	panic(fmt.Errorf("expr: unknown operator %s", op))
}

func operateStrings(op lex.Token, av, bv value.StringValue) value.Value {

	//  Any other ops besides eq/not ?
	a, b := av.Val(), bv.Val()
	switch op.T {
	case lex.TokenEqualEqual, lex.TokenEqual: //  ==
		//u.Infof("==?  %v  %v", av, bv)
		if a == b {
			return value.BoolValueTrue
		}
		return value.BoolValueFalse

	case lex.TokenNE: //  !=
		//u.Infof("!=?  %v  %v", av, bv)
		if a == b {
			return value.BoolValueFalse
		}
		return value.BoolValueTrue

	case lex.TokenLike: // a(value) LIKE b(pattern)
		match, err := glob.Match(b, a)
		if err != nil {
			value.NewErrorValuef("invalid LIKE pattern: %q", a)
		}
		if match {
			return value.BoolValueTrue
		}
		return value.BoolValueFalse
	}
	return value.NewErrorValuef("unsupported operator for strings: %s", op.T)
}

func operateInts(op lex.Token, av, bv value.IntValue) value.Value {
	//if math.IsNaN(a) || math.IsNaN(b) {
	//	return math.NaN()
	//}
	a, b := av.Val(), bv.Val()
	//u.Infof("a op b:   %v %v %v", a, op.V, b)
	switch op.T {
	case lex.TokenPlus: // +
		//r = a + b
		return value.NewIntValue(a + b)
	case lex.TokenStar, lex.TokenMultiply: // *
		//r = a * b
		return value.NewIntValue(a * b)
	case lex.TokenMinus: // -
		//r = a - b
		return value.NewIntValue(a - b)
	case lex.TokenDivide: //    /
		//r = a / b
		//u.Debugf("divide:   %v / %v = %v", a, b, a/b)
		return value.NewIntValue(a / b)
	case lex.TokenModulus: //    %
		//r = a / b
		//u.Debugf("modulus:   %v / %v = %v", a, b, a/b)
		return value.NewIntValue(a % b)

	// Below here are Boolean Returns
	case lex.TokenEqualEqual: //  ==
		if a == b {
			return value.BoolValueTrue
		} else {
			return value.BoolValueFalse
		}
	case lex.TokenGT: //  >
		if a > b {
			return value.BoolValueTrue
		} else {
			return value.BoolValueFalse
		}
	case lex.TokenNE: //  !=    or <>
		if a != b {
			return value.BoolValueTrue
		} else {
			return value.BoolValueFalse
		}
	case lex.TokenLT: // <
		if a < b {
			return value.BoolValueTrue
		} else {
			return value.BoolValueFalse
		}
	case lex.TokenGE: // >=
		if a >= b {
			return value.BoolValueTrue
		} else {
			return value.BoolValueFalse
		}
	case lex.TokenLE: // <=
		if a <= b {
			return value.BoolValueTrue
		} else {
			return value.BoolValueFalse
		}
	case lex.TokenLogicOr, lex.TokenOr: //  ||
		if a != 0 || b != 0 {
			return value.BoolValueTrue
		} else {
			return value.BoolValueFalse
		}
	case lex.TokenLogicAnd: //  &&
		if a != 0 && b != 0 {
			return value.BoolValueTrue
		} else {
			return value.BoolValueFalse
		}
	}
	panic(fmt.Errorf("expr: unknown operator %s", op))
}

func uoperate(op string, a float64) (r float64) {
	switch op {
	case "!":
		if a == 0 {
			r = 1
		} else {
			r = 0
		}
	case "-":
		r = -a
	default:
		panic(fmt.Errorf("expr: unknown operator %s", op))
	}
	return
}
