package dotnet

import (
	"bytes"
	"fmt"
	"github.com/pulumi/pulumi/pkg/v2/codegen/schema"
	"io"
	"math/big"
	"strings"

	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclsyntax"
	"github.com/pulumi/pulumi/pkg/v2/codegen/hcl2"
	"github.com/pulumi/pulumi/pkg/v2/codegen/hcl2/model"
	"github.com/pulumi/pulumi/sdk/v2/go/common/util/contract"
	"github.com/zclconf/go-cty/cty"
)

type nameInfo int

func (nameInfo) Format(name string) string {
	return cleanName(name)
}

func (g *generator) processExpression(expr model.Expression, name string) model.Expression {
	// TODO(pdg): diagnostics
	expr, _ = hcl2.RewriteApplies(expr, nameInfo(0), true)
	expr, _ = g.applyConstructorCalls(expr, name)
	return expr
}


func (g *generator) applyConstructorCalls(expr model.Expression, name string) (model.Expression, hcl.Diagnostics) {
	currentName := name
	pre := func(expr model.Expression) (model.Expression, hcl.Diagnostics) {
		switch expr := expr.(type) {
		case *model.FunctionCallExpression:
			switch expr.Name {
			case "invoke":
				pkg, module, fn, diags := g.functionName(expr.Args[0])
				contract.Assert(len(diags) == 0)
				if module != "" {
					module = "." + Title(module)
				}
				currentName = fmt.Sprintf("%s%s.%s", pkg, module, fn)
			}
		}
		return expr, nil
	}
	post := func(expr model.Expression) (model.Expression, hcl.Diagnostics) {
		switch expr := expr.(type) {
		case *model.ObjectConsExpression:
			return newConstructorCall(expr, currentName + "Args"), nil
		default:
			return expr, nil
		}
	}
	return model.VisitExpression(expr, pre, post)
}

func (g *generator) GetPrecedence(expr model.Expression) int {
	// Precedence is derived from
	// TODO (msh): Current values copied from Node, update based on
	// https://docs.microsoft.com/en-us/dotnet/csharp/language-reference/operators/
	switch expr := expr.(type) {
	case *model.ConditionalExpression:
		return 4
	case *model.BinaryOpExpression:
		switch expr.Operation {
		case hclsyntax.OpLogicalOr:
			return 5
		case hclsyntax.OpLogicalAnd:
			return 6
		case hclsyntax.OpEqual, hclsyntax.OpNotEqual:
			return 11
		case hclsyntax.OpGreaterThan, hclsyntax.OpGreaterThanOrEqual, hclsyntax.OpLessThan,
			hclsyntax.OpLessThanOrEqual:
			return 12
		case hclsyntax.OpAdd, hclsyntax.OpSubtract:
			return 14
		case hclsyntax.OpMultiply, hclsyntax.OpDivide, hclsyntax.OpModulo:
			return 15
		default:
			contract.Failf("unexpected binary expression %v", expr)
		}
	case *model.UnaryOpExpression:
		return 17
	case *model.FunctionCallExpression:
		switch expr.Name {
		case intrinsicAwait:
			return 17
		default:
			return 20
		}
	case *model.ForExpression, *model.IndexExpression, *model.RelativeTraversalExpression, *model.SplatExpression,
		*model.TemplateJoinExpression:
		return 20
	case *model.AnonymousFunctionExpression, *model.LiteralValueExpression, *model.ObjectConsExpression,
		*model.ScopeTraversalExpression, *model.TemplateExpression, *model.TupleConsExpression:
		return 22
	default:
		contract.Failf("unexpected expression %v of type %T", expr, expr)
	}
	return 0
}

func (g *generator) GenAnonymousFunctionExpression(w io.Writer, expr *model.AnonymousFunctionExpression) {
	switch len(expr.Signature.Parameters) {
	case 0:
		g.Fgen(w, "()")
	case 1:
		g.Fgenf(w, "%s", expr.Signature.Parameters[0].Name)
		g.Fgenf(w, " => %.v", expr.Body)
	default:
		g.Fgen(w, "values =>\n")
		g.Fgenf(w, "%s{\n", g.Indent)
		g.Indented(func() {
			for i, p := range expr.Signature.Parameters {
				g.Fgenf(w, "%svar %s = values.Item%v;\n", g.Indent, p.Name, i+1)
			}
			g.Fgenf(w, "%sreturn %.v;\n", g.Indent, expr.Body)
		})
		g.Fgenf(w, "%s}", g.Indent)
	}
}

func (g *generator) GenBinaryOpExpression(w io.Writer, expr *model.BinaryOpExpression) {
	opstr, precedence := "", g.GetPrecedence(expr)
	switch expr.Operation {
	case hclsyntax.OpAdd:
		opstr = "+"
	case hclsyntax.OpDivide:
		opstr = "/"
	case hclsyntax.OpEqual:
		opstr = "=="
	case hclsyntax.OpGreaterThan:
		opstr = ">"
	case hclsyntax.OpGreaterThanOrEqual:
		opstr = ">="
	case hclsyntax.OpLessThan:
		opstr = "<"
	case hclsyntax.OpLessThanOrEqual:
		opstr = "<="
	case hclsyntax.OpLogicalAnd:
		opstr = "&&"
	case hclsyntax.OpLogicalOr:
		opstr = "||"
	case hclsyntax.OpModulo:
		opstr = "%"
	case hclsyntax.OpMultiply:
		opstr = "*"
	case hclsyntax.OpNotEqual:
		opstr = "!="
	case hclsyntax.OpSubtract:
		opstr = "-"
	default:
		opstr, precedence = ",", 1
	}

	g.Fgenf(w, "%.[1]*[2]v %[3]v %.[1]*[4]o", precedence, expr.LeftOperand, opstr, expr.RightOperand)
}

func (g *generator) GenConditionalExpression(w io.Writer, expr *model.ConditionalExpression) {
	g.Fgenf(w, "%.4v ? %.4v : %.4v", expr.Condition, expr.TrueResult, expr.FalseResult)
}

func (g *generator) GenForExpression(w io.Writer, expr *model.ForExpression) {
	g.genNYI(w, "ForExpression")
}

func isOutputOrPromise(t model.Type) bool {
	switch t.(type) {
	case *model.OutputType:
		return true
	case *model.PromiseType:
		return true
	}
	return false
}

func (g *generator) genApply(w io.Writer, expr *model.FunctionCallExpression) {
	// Extract the list of outputs and the continuation expression from the `__apply` arguments.
	applyArgs, then := hcl2.ParseApplyCall(expr)

	if len(applyArgs) == 1 {
		// If we only have a single output, just generate a normal `.Apply`
		if isOutputOrPromise(applyArgs[0].Type()) {
			// We treat promises as outputs by immediately converting them at instantiation time.
			g.Fgenf(w, "%.20v", applyArgs[0])
		} else {
			// If it's not an output yet, create one.
			g.Fgenf(w, "Output.Create(%.20v)", applyArgs[0])
		}
		g.Fgenf(w, ".Apply(%.v)", then)
	} else {
		// Otherwise, generate a call to `Output.Tuple().Apply()`.
		g.Fgen(w, "Output.Tuple(")
		for i, o := range applyArgs {
			if i > 0 {
				g.Fgen(w, ", ")
			}
			g.Fgenf(w, "%.v", o)
		}

		g.Fgenf(w, ").Apply(%.v)", then)
	}
}

func (g *generator) genRange(w io.Writer, call *model.FunctionCallExpression, entries bool) {
	g.genNYI(w, "Range %v %v", call, entries)
}

var functionNamespaces = map[string][]string{
	"readFile": {"System.IO"},
	"readDir":  {"System.IO", "System.Linq"},
	"toJSON":   {"System.Text.Json"},
}

func (g *generator) genFunctionUsings(x *model.FunctionCallExpression) []string {
	if x.Name != "invoke" {
		return functionNamespaces[x.Name]
	}

	pkg, _, _, diags := g.functionName(x.Args[0])
	contract.Assert(len(diags) == 0)
	return []string{fmt.Sprintf("%s = Pulumi.%[1]s", pkg)}
}

func (g *generator) GenFunctionCallExpression(w io.Writer, expr *model.FunctionCallExpression) {
	switch expr.Name {
	case hcl2.IntrinsicConvert:
		switch arg := expr.Args[0].(type) {
		case *model.ObjectConsExpression:
			g.genObjectConsExpression(w, arg, expr.Type())
		default:
			g.Fgenf(w, "%.v", expr.Args[0]) // <- probably wrong w.r.t. precedence
		}
	case hcl2.IntrinsicApply:
		g.genApply(w, expr)
	case intrinsicAwait:
		g.Fgenf(w, "await %.17v", expr.Args[0])
	case intrinsicConstructor:
		if name, ok := expr.Args[0].(*model.LiteralValueExpression); ok {
			g.Fgenf(w, "new %s%.v", name.Value.AsString(), expr.Args[1])
		}
	case "element":
		g.genNYI(w, "element")
	case "entries":
		switch expr.Args[0].Type().(type) {
		case *model.ListType, *model.TupleType:
			if call, ok := expr.Args[0].(*model.FunctionCallExpression); ok && call.Name == "range" {
				g.genRange(w, call, true)
				return
			}
			g.Fgenf(w, "%.20v.Select((v, k)", expr.Args[0])
		case *model.MapType, *model.ObjectType:
			g.genNYI(w, "MapOrObjectEntries")
		}
		g.Fgenf(w, " => new { Key = k, Value = v })")
	case "fileArchive":
		g.Fgenf(w, "new FileArchive(%.v)", expr.Args[0])
	case "fileAsset":
		g.Fgenf(w, "new FileAsset(%.v)", expr.Args[0])
	case "invoke":
		pkg, module, fn, diags := g.functionName(expr.Args[0])
		contract.Assert(len(diags) == 0)
		if module != "" {
			module = "." + module
		}
		name := fmt.Sprintf("%s%s.%s", pkg, module, fn)

		optionsBag := ""
		if len(expr.Args) == 3 {
			var buf bytes.Buffer
			g.Fgenf(&buf, ", %.v", expr.Args[2])
			optionsBag = buf.String()
		}

		//annotations := expr.Signature.Parameters[1].Type.GetAnnotations()
		//token := annotations[0].(*schema.ObjectType)
		//tokenRange := expr.Signature.Parameters[1].Type.SyntaxNode().Range()
		//pkg, module, member, _ := hcl2.DecomposeToken(token.Token, tokenRange)
		//pkg, module, fn = Title(cleanName(pkg)), Title(strings.Replace(module, "/", ".", -1)), Title(member)
		//argsName := fmt.Sprintf("%s%s.%s", pkg, module, fn)

		g.Fgenf(w, "Output.Create(%s.InvokeAsync(%.v%v))", name, expr.Args[1], optionsBag)
	case "length" +
		"":
		g.Fgenf(w, "%.20v.Length", expr.Args[0])
	case "lookup":
		g.genNYI(w, "Lookup")
	case "range":
		g.genRange(w, expr, false)
	case "readFile":
		g.genNYI(w, "ReadFile")
	case "readDir":
		g.Fgenf(w, "Directory.GetFiles(%v).Select(Path.GetFileName)", expr.Args[0])
	case "split":
		g.Fgenf(w, "%.20v.Split(%v)", expr.Args[1], expr.Args[0])
	case "toJSON":
		g.Fgenf(w, "JsonSerializer.Serialize(%v)", expr.Args[0])
	default:
		var rng hcl.Range
		if expr.Syntax != nil {
			rng = expr.Syntax.Range()
		}
		g.genNYI(w, "FunctionCallExpression: %v (%v)", expr.Name, rng)
	}
}

func (g *generator) GenIndexExpression(w io.Writer, expr *model.IndexExpression) {
	g.Fgenf(w, "%.20v[%.v]", expr.Collection, expr.Key)
}

func (g *generator) escapeString(v string, verbatim bool) string {
	builder := strings.Builder{}
	for _, c := range v {
		if verbatim {
			if c == '"' {
				builder.WriteRune('"')
			}
		} else {
			if c == '"' || c == '\\' {
				builder.WriteRune('\\')
			}
		}
		builder.WriteRune(c)
	}
	return builder.String()
}

func (g *generator) genStringLiteral(w io.Writer, v string) {
	newlines := strings.Contains(v, "\n")
	if !newlines {
		// This string does not contain newlines so we'll generate a regular string literal. Quotes and backslashes
		// will be escaped in conformance with
		// https://docs.microsoft.com/en-us/dotnet/csharp/language-reference/language-specification/lexical-structure#string-literals
		g.Fgen(w, "\"")
		g.Fgen(w, g.escapeString(v, false))
		g.Fgen(w, "\"")
	} else {
		// This string does contain newlines, so we'll generate a verbatim string literal. Quotes will be escaped
		// in conformance with
		// https://docs.microsoft.com/en-us/dotnet/csharp/language-reference/language-specification/lexical-structure#string-literals
		g.Fgen(w, "@\"")
		g.Fgen(w, g.escapeString(v, true))
		g.Fgen(w, "\"")
	}
}

func (g *generator) GenLiteralValueExpression(w io.Writer, expr *model.LiteralValueExpression) {
	switch expr.Type() {
	case model.BoolType:
		g.Fgenf(w, "%v", expr.Value.True())
	case model.NumberType:
		bf := expr.Value.AsBigFloat()
		if i, acc := bf.Int64(); acc == big.Exact {
			g.Fgenf(w, "%d", i)
		} else {
			f, _ := bf.Float64()
			g.Fgenf(w, "%g", f)
		}
	case model.StringType:
		g.genStringLiteral(w, expr.Value.AsString())
	default:
		contract.Failf("unexpected literal type in GenLiteralValueExpression: %v (%v)", expr.Type(),
			expr.SyntaxNode().Range())
	}
}

func (g *generator) GenObjectConsExpression(w io.Writer, expr *model.ObjectConsExpression) {
	g.genObjectConsExpression(w, expr, expr.Type())
}

func (g *generator) genObjectConsExpression(w io.Writer, expr *model.ObjectConsExpression, destType model.Type) {	if len(expr.Items) == 0 {
		g.Fgen(w, "{}")
	} else {
		// Extract object schema from type annotations
		var objectType *schema.ObjectType
		if annotations := destType.GetAnnotations(); len(annotations) == 1 {
			objectType = annotations[0].(*schema.ObjectType)
			panic(objectType)
		}

		g.Fgenf(w, "\n%s{\n", g.Indent)
		g.Indented(func() {
			for _, item := range expr.Items {

				g.Fgenf(w, "%s", g.Indent)
				if lit, isLit := item.Key.(*model.LiteralValueExpression); isLit {
					g.Fprint(w, Title(lit.Value.AsString()))
					g.Fgenf(w, " = %.v,\n", item.Value)
				} else {
					// TODO (msh): We can't mix those, we should put more distinction between object initializers and dictionaries.
					g.Fgenf(w, "{ %.v, %.v },\n", item.Key, item.Value)
				}
			}
		})
		g.Fgenf(w, "%s}", g.Indent)
	}
}

func (g *generator) genRelativeTraversal(w io.Writer, traversal hcl.Traversal, parts []model.Traversable) {
	for i, part := range traversal {
		var key cty.Value
		switch part := part.(type) {
		case hcl.TraverseAttr:
			key = cty.StringVal(part.Name)
		case hcl.TraverseIndex:
			key = part.Key
		default:
			contract.Failf("unexpected traversal part of type %T (%v)", part, part.SourceRange())
		}

		if model.IsOptionalType(model.GetTraversableType(parts[i])) {
			g.Fgen(w, "?")
		}

		switch key.Type() {
		case cty.String:
			g.Fgenf(w, ".%s", Title(key.AsString()))
		case cty.Number:
			idx, _ := key.AsBigFloat().Int64()
			g.Fgenf(w, "[%d]", idx)
		default:
			contract.Failf("unexpected traversal key of type %T (%v)", key, key.AsString())
		}
	}
}

func (g *generator) GenRelativeTraversalExpression(w io.Writer, expr *model.RelativeTraversalExpression) {
	g.Fgenf(w, "%.20v", expr.Source)
	g.genRelativeTraversal(w, expr.Traversal, expr.Parts)
}

func (g *generator) GenScopeTraversalExpression(w io.Writer, expr *model.ScopeTraversalExpression) {
	rootName := expr.RootName
	if _, ok := expr.Parts[0].(*model.SplatVariable); ok {
		// TODO (msh): Figure out what to do here.
		rootName = "__item"
	}

	g.Fgen(w, rootName)
	g.genRelativeTraversal(w, expr.Traversal.SimpleSplit().Rel, expr.Parts)
}

func (g *generator) GenSplatExpression(w io.Writer, expr *model.SplatExpression) {
	g.Fgenf(w, "%.20v.Select(v => v.%.v)", expr.Source, expr.Each)
}

func (g *generator) GenTemplateExpression(w io.Writer, expr *model.TemplateExpression) {
	multiLine := false
	hasExpressions := false
	for _, expr := range expr.Parts {
		if lit, ok := expr.(*model.LiteralValueExpression); ok && lit.Type() == model.StringType {
			if strings.Contains(lit.Value.AsString(), "\n") {
				multiLine = true
			}
		} else {
			hasExpressions = true
		}
	}

	if multiLine {
		g.Fgen(w, "@")
	}
	if hasExpressions {
		g.Fgen(w, "$")
	}
	g.Fgen(w, "\"")
	for _, expr := range expr.Parts {
		if lit, ok := expr.(*model.LiteralValueExpression); ok && lit.Type() == model.StringType {
			g.Fgen(w, g.escapeString(lit.Value.AsString(), multiLine))
		} else {
			g.Fgenf(w, "{%.v}", expr)
		}
	}
	g.Fgen(w, "\"")
}

func (g *generator) GenTemplateJoinExpression(w io.Writer, expr *model.TemplateJoinExpression) {
	g.genNYI(w, "TemplateJoinExpression")
}

func (g *generator) GenTupleConsExpression(w io.Writer, expr *model.TupleConsExpression) {
	switch len(expr.Expressions) {
	case 0:
		g.Fgen(w, "{}")
	default:
		g.Fgenf(w, "\n%s{", g.Indent)
		g.Indented(func() {
			for _, v := range expr.Expressions {
				g.Fgenf(w, "\n%s%.v,", g.Indent, v)
			}
		})
		g.Fgenf(w, "\n%s}", g.Indent)
	}
}

func (g *generator) GenUnaryOpExpression(w io.Writer, expr *model.UnaryOpExpression) {
	opstr, precedence := "", g.GetPrecedence(expr)
	switch expr.Operation {
	case hclsyntax.OpLogicalNot:
		opstr = "!"
	case hclsyntax.OpNegate:
		opstr = "-"
	}
	g.Fgenf(w, "%[2]v%.[1]*[3]v", precedence, opstr, expr.Operand)
}
