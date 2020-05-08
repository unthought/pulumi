// Copyright 2016-2020, Pulumi Corporation.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package dotnet

import (
	"bytes"
	"fmt"
	"github.com/pulumi/pulumi/pkg/v2/codegen/schema"
	"github.com/pulumi/pulumi/sdk/v2/go/common/util/contract"
	"io"
	"sort"
	"strings"

	"github.com/hashicorp/hcl/v2"
	"github.com/pulumi/pulumi/pkg/v2/codegen"
	"github.com/pulumi/pulumi/pkg/v2/codegen/hcl2"
	"github.com/pulumi/pulumi/pkg/v2/codegen/hcl2/model"
	"github.com/pulumi/pulumi/pkg/v2/codegen/hcl2/model/format"
	"github.com/pulumi/pulumi/pkg/v2/codegen/hcl2/syntax"
)

type generator struct {
	// The formatter to use when generating code.
	*format.Formatter

	program     *hcl2.Program
	namespaces  map[string]map[string]string
	diagnostics hcl.Diagnostics

	configCreated bool
}

func GenerateProgram(program *hcl2.Program) (map[string][]byte, hcl.Diagnostics, error) {
	// Linearize the nodes into an order appropriate for procedural code generation.
	nodes := hcl2.Linearize(program)

	// Import C#-specific schema info.
	namespaces := make(map[string]map[string]string)
	for _, p := range program.Packages() {
		if err := p.ImportLanguages(map[string]schema.Language{"csharp": Importer}); err != nil {
			return make(map[string][]byte), nil, err
		}

		packageNamespaces := p.Language["csharp"].(CSharpPackageInfo).Namespaces
		namespaces[p.Name] = packageNamespaces
	}

	g := &generator{
		program: program,
		namespaces: namespaces,
	}
	g.Formatter = format.NewFormatter(g)

	var index bytes.Buffer
	g.genPreamble(&index, program)
	g.Indented(func() {
		g.Indented(func() {
			for _, n := range nodes {
				g.genNode(&index, n)
			}
		})
	})
	g.genPostamble(&index, nodes)

	files := map[string][]byte{
		"MyStack.cs": index.Bytes(),
	}
	return files, g.diagnostics, nil
}

// genLeadingTrivia generates the list of leading trivia assicated with a given token.
func (g *generator) genLeadingTrivia(w io.Writer, token syntax.Token) {
	// TODO(pdg): whitespace?
	for _, t := range token.LeadingTrivia {
		if c, ok := t.(syntax.Comment); ok {
			g.genComment(w, c)
		}
	}
}

// genTrailingTrivia generates the list of trailing trivia assicated with a given token.
func (g *generator) genTrailingTrivia(w io.Writer, token syntax.Token) {
	// TODO(pdg): whitespace
	for _, t := range token.TrailingTrivia {
		if c, ok := t.(syntax.Comment); ok {
			g.genComment(w, c)
		}
	}
}

// genTrivia generates the list of trivia assicated with a given token.
func (g *generator) genTrivia(w io.Writer, token syntax.Token) {
	g.genLeadingTrivia(w, token)
	g.genTrailingTrivia(w, token)
}

// genComment generates a comment into the output.
func (g *generator) genComment(w io.Writer, comment syntax.Comment) {
	for _, l := range comment.Lines {
		g.Fgenf(w, "%s//%s\n", g.Indent, l)
	}
}

func (g *generator) genPreamble(w io.Writer, program *hcl2.Program) {
	// Print the Pulumi using at the top.
	g.Fprintln(w, `using Pulumi;`)

	// Accumulate other using statements for the various providers and packages. Don't emit them yet, as we need
	// to sort them later on.
	importSet := codegen.NewStringSet("Pulumi")
	for _, n := range program.Nodes {
		if r, isResource := n.(*hcl2.Resource); isResource {
			pkg, _, _, _ := r.DecomposeToken()
			importSet.Add(fmt.Sprintf("%s = Pulumi.%[1]s", Title(pkg)))
			if r.Options != nil && r.Options.Range != nil {
				importSet.Add("System.Collections.Generic")
			}
		}
		diags := n.VisitExpressions(nil, func(n model.Expression) (model.Expression, hcl.Diagnostics) {
			if call, ok := n.(*model.FunctionCallExpression); ok {
				for _, i := range g.genFunctionUsings(call) {
					if i != "" {
						importSet.Add(i)
					}
				}
			}
			return n, nil
		})
		contract.Assert(len(diags) == 0)
	}

	var imports []string
	for _, pkg := range importSet.SortedValues() {
		if pkg == "Pulumi" {
			continue
		}
		imports = append(imports, fmt.Sprintf("using %v;", pkg))
	}
	sort.Strings(imports)

	// Now sort the imports and emit them.
	for _, i := range imports {
		g.Fprintln(w, i)
	}
	g.Fprint(w, "\n")

	// Emit Stack class signature
	g.Fprint(w, "class MyStack : Stack\n")
	g.Fprint(w, "{\n")
	g.Fprint(w, "    public MyStack()\n")
	g.Fprint(w, "    {\n")
}

func (g *generator) genPostamble(w io.Writer, nodes []hcl2.Node) {
	g.Indented(func() {
		// Close class constructor
		g.Fprintf(w, "%s}\n\n", g.Indent)

		// Emit stack output properties
		for _, n := range nodes {
			switch n := n.(type) {
			case *hcl2.OutputVariable:
				g.genOutputProperty(w, n)
			}
		}
	})
	g.Fprint(w, "}\n")
}

func (g *generator) genNode(w io.Writer, n hcl2.Node) {
	switch n := n.(type) {
	case *hcl2.Resource:
		g.genResource(w, n)
	case *hcl2.ConfigVariable:
		g.genConfigVariable(w, n)
	case *hcl2.LocalVariable:
		g.genLocalVariable(w, n)
	case *hcl2.OutputVariable:
		g.genOutputAssignment(w, n)
	}
}

// resourceTypeName computes the C# package, module, and type name for the given resource.
func (g *generator) resourceTypeName(r *hcl2.Resource) (string, string, string, hcl.Diagnostics) {
	// Compute the resource type from the Pulumi type token.
	pkg, module, member, diagnostics := r.DecomposeToken()
	if pkg == "pulumi" && module == "providers" {
		pkg, module, member = member, "", "Provider"
	}

	namespaces := g.namespaces[pkg]
	namespaceKey := strings.Replace(module, "/", ".", -1)
	rootNamespace := namespaceName(namespaces, pkg)
	namespace := namespaceName(namespaces, namespaceKey)
	return rootNamespace, namespace, Title(member), diagnostics
}

// functionName computes the .NET package, module, and name for the given function token.
func (g *generator) functionName(tokenArg model.Expression) (string, string, string, hcl.Diagnostics) {
	token := tokenArg.(*model.TemplateExpression).Parts[0].(*model.LiteralValueExpression).Value.AsString()
	tokenRange := tokenArg.SyntaxNode().Range()

	// Compute the resource type from the Pulumi type token.
	pkg, module, member, diagnostics := hcl2.DecomposeToken(token, tokenRange)
	namespaces := g.namespaces[pkg]
	namespaceKey := strings.Replace(module, "/", ".", -1)
	rootNamespace := namespaceName(namespaces, pkg)
	namespace := namespaceName(namespaces, namespaceKey)
	return rootNamespace, namespace, Title(member), diagnostics
}

// makeResourceName returns the expression that should be emitted for a resource's "name" parameter given its base name
// and the count variable name, if any.
func (g *generator) makeResourceName(baseName, count string) string {
	if count == "" {
		return fmt.Sprintf(`"%s"`, baseName)
	}
	return fmt.Sprintf("$\"%s-{%s}\"", baseName, count)
}

// genResource handles the generation of instantiations of non-builtin resources.
func (g *generator) genResource(w io.Writer, r *hcl2.Resource) {
	pkg, module, memberName, diagnostics := g.resourceTypeName(r)
	g.diagnostics = append(g.diagnostics, diagnostics...)

	anns := model.ResolveOutputs(r.InputType).GetAnnotations()
	if (len(anns) > 0) {
		panic(anns)
	}

	// Add conversions to input properties
	for _, input := range r.Inputs {
		destType, diags := r.InputType.Traverse(hcl.TraverseAttr{Name: input.Name})
		contract.Ignore(diags)
		input.Value = hcl2.RewriteConversions(input.Value, destType.(model.Type))
	}

	if module != "" {
		module = "." + module
	}

	qualifiedMemberName := fmt.Sprintf("%s%s.%s", pkg, module, memberName)

	optionsBag := ""

	name := r.Name()

	g.genTrivia(w, r.Definition.Tokens.GetType(""))
	for _, l := range r.Definition.Tokens.GetLabels(nil) {
		g.genTrivia(w, l)
	}
	g.genTrivia(w, r.Definition.Tokens.GetOpenBrace())

	instantiate := func(resName string) {
		g.Fgenf(w, "new %s(%s, new %[1]sArgs\n", qualifiedMemberName, resName)
		g.Fgenf(w, "%s{\n", g.Indent)
		g.Indented(func() {
			for _, attr := range r.Inputs {
				propertyName := Title(attr.Name)
				g.Fgenf(w, "%s%s =", g.Indent, propertyName);
				g.Fgenf(w, " %.v,\n", g.processExpression(attr.Value, qualifiedMemberName))
			}
		})
		g.Fgenf(w, "%s}%s)", g.Indent, optionsBag)
	}

	if r.Options != nil && r.Options.Range != nil {
		if model.InputType(model.BoolType).ConversionFrom(r.Options.Range.Type()) == model.SafeConversion {
			g.genNYI(w, "OptionsRangeSafeConversion %v", r.Options.Range)
		} else {
			rangeExpr := newAwaitCall(r.Options.Range)
			g.Fgenf(w, "%svar %s = new List<%s>();\n", g.Indent, name, qualifiedMemberName)

			if model.InputType(model.NumberType).ConversionFrom(rangeExpr.Type()) != model.NoConversion {
				g.genNYI(w, "OptionsRangeConversion %v", rangeExpr)
			} else {
				rangeExpr := &model.FunctionCallExpression{
					Name: "entries",
					Args: []model.Expression{rangeExpr},
				}
				g.Fgenf(w, "%sforeach (var range in %.v)\n", g.Indent, rangeExpr)
				g.Fgenf(w, "%s{\n", g.Indent)
			}

			resName := g.makeResourceName(name, "range.Key")
			g.Indented(func() {
				g.Fgenf(w, "%s%s.Add(", g.Indent, name)
				instantiate(resName)
				g.Fgenf(w, ");\n")
			})
			g.Fgenf(w, "%s}\n", g.Indent)
		}
	} else {
		g.Fgenf(w, "%svar %s = ", g.Indent, name)
		instantiate(g.makeResourceName(name, ""))
		g.Fgenf(w, ";\n")
	}

	g.genTrivia(w, r.Definition.Tokens.GetCloseBrace())
}

func (g *generator) genConfigVariable(w io.Writer, v *hcl2.ConfigVariable) {
	g.genNYI(w, "Config")
}

func (g *generator) genLocalVariable(w io.Writer, v *hcl2.LocalVariable) {
	// TODO(pdg): trivia
	g.Fgenf(w, "%svar %s = %.3v;\n", g.Indent, v.Name(), g.processExpression(v.Definition.Value, ""))
}

func (g *generator) genOutputAssignment(w io.Writer, v *hcl2.OutputVariable) {
	// TODO(pdg): trivia
	g.Fgenf(w, "%sthis.%s = %.3v;\n", g.Indent, Title(v.Name()), g.processExpression(v.Value, ""))
}

func (g *generator) genOutputProperty(w io.Writer, v *hcl2.OutputVariable) {
	// TODO(pdg): trivia
	g.Fgenf(w, "%s[Output(\"%s\")] public Output<string> %s { get; set; }\n", g.Indent, v.Name(), Title(v.Name()))
}

func (g *generator) genNYI(w io.Writer, reason string, vs ...interface{}) {
	g.Fgenf(w, "/* TODO (%q)*/", fmt.Sprintf(reason, vs...))
}
