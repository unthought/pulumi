package docs

import (
	"strings"

	"github.com/pulumi/pulumi/pkg/v2/codegen"
)

const defaultMissingExampleSnippetPlaceholder = "Coming soon!"

type exampleSection struct {
	Title string
	// Snippets is a map of language to its code snippet, if any.
	Snippets map[string]string
}

func extractExampleCodeSnippets(exampleContent string) map[string]string {
	snippets := map[string]string{}
	for _, lang := range supportedLanguages {
		var snippet string
		if lang == "nodejs" {
			lang = "typescript"
		}
		codeFence := "```" + lang
		langSnippetIndex := strings.Index(exampleContent, codeFence)
		// If there is no snippet for the provided language in this example,
		// then just return nil.
		if langSnippetIndex < 0 {
			snippet = defaultMissingExampleSnippetPlaceholder
			continue
		}

		switch lang {
		case "csharp":
			snippet = codegen.CSharpCodeSnippetRE.FindString(exampleContent)
		case "go":
			snippet = codegen.GoCodeSnippetRE.FindString(exampleContent)
		case "python":
			snippet = codegen.PythonCodeSnippetRE.FindString(exampleContent)
		case "typescript":
			snippet = codegen.TSCodeSnippetRE.FindString(exampleContent)
		}

		snippets[lang] = snippet
	}

	return snippets
}

func getExampleSections(examplesContent string) []exampleSection {
	examples := make([]exampleSection, 0)
	exampleMatches := codegen.GetAllMatchedGroupsFromRegex(codegen.IndividualExampleRE, examplesContent)
	if matchedExamples, ok := exampleMatches["example_content"]; ok {
		for _, ex := range matchedExamples {
			snippets := extractExampleCodeSnippets(ex)
			if snippets == nil || len(snippets) == 0 {
				continue
			}

			examples = append(examples, exampleSection{
				Title:    codegen.H3TitleRE.FindString(ex),
				Snippets: snippets,
			})
		}
	}
	return examples
}

func processExamples(descriptionWithExamples string) ([]exampleSection, error) {
	if descriptionWithExamples == "" {
		return nil, nil
	}

	// Get the content enclosing the outer examples short code.
	examplesContent := codegen.ExtractExamplesSection(descriptionWithExamples)
	if examplesContent == nil {
		return nil, nil
	}

	// Within the examples section, identify each example section
	// which is wrapped in a {{% example %}} shortcode.
	return getExampleSections(*examplesContent), nil
}
