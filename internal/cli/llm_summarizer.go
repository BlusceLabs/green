package cli

import (
	"context"
	"fmt"
	"strings"

	"github.com/BlusceLabs/green/internal/config"
	"github.com/BlusceLabs/green/internal/greenruntime"
	"github.com/BlusceLabs/green/internal/learning"
)

// providerSummarizer adapts a greenruntime.Provider into the learning loop's
// Summarizer hook. It asks the model to refine a draft knowledge artifact
// (memory, skill description, note) into a single improved, concise version.
func providerSummarizer(provider greenruntime.Provider) learning.Summarizer {
	return func(text string) (string, error) {
		if strings.TrimSpace(text) == "" {
			return text, nil
		}
		prompt := "You are an editor that refines agent-generated knowledge artifacts. " +
			"Given a draft memory, skill description, or note, return a single improved version: " +
			"concise, clearly stated, free of meta-commentary. Output only the refined text.\n\n" +
			"Draft:\n" + text
		events, err := provider.StreamCompletion(context.Background(), greenruntime.CompletionRequest{
			Messages: []greenruntime.Message{
				{Role: greenruntime.MessageRoleUser, Content: prompt},
			},
		})
		if err != nil {
			return "", fmt.Errorf("llm summarize: %w", err)
		}
		collected := greenruntime.CollectStream(context.Background(), events)
		if collected.Error != "" {
			return "", fmt.Errorf("llm summarize: %s", collected.Error)
		}
		return strings.TrimSpace(collected.Text), nil
	}
}

// resolveActiveProvider builds the currently-configured runtime provider so the
// learning loop / recall can upgrade extraction and synthesis with a model.
func resolveActiveProvider(deps appDeps) (greenruntime.Provider, error) {
	wd, err := deps.getwd()
	if err != nil {
		return nil, fmt.Errorf("resolve provider: %w", err)
	}
	resolved, err := deps.resolveConfig(wd, config.Overrides{})
	if err != nil {
		return nil, fmt.Errorf("resolve provider: %w", err)
	}
	if resolved.Provider.Name == "" {
		return nil, fmt.Errorf("no active provider configured (run `green setup` or `green provider use <name>`)")
	}
	return deps.newProvider(resolved.Provider)
}

// synthesizeRecall asks the model to answer the recall query using only the
// supplied past-session excerpts, returning a single synthesized answer.
func synthesizeRecall(provider greenruntime.Provider, query string, excerpts []string) (string, error) {
	if len(excerpts) == 0 {
		return "", nil
	}
	var b strings.Builder
	b.WriteString("Answer the user's question using ONLY the following excerpts from past sessions. " +
		"If the excerpts do not contain the answer, say so plainly. Be concise.\n\n")
	b.WriteString("Question: " + query + "\n\nExcerpts:\n")
	for i, ex := range excerpts {
		b.WriteString(fmt.Sprintf("[%d] %s\n", i+1, ex))
	}
	events, err := provider.StreamCompletion(context.Background(), greenruntime.CompletionRequest{
		Messages: []greenruntime.Message{
			{Role: greenruntime.MessageRoleUser, Content: b.String()},
		},
	})
	if err != nil {
		return "", fmt.Errorf("llm recall: %w", err)
	}
	collected := greenruntime.CollectStream(context.Background(), events)
	if collected.Error != "" {
		return "", fmt.Errorf("llm recall: %s", collected.Error)
	}
	return strings.TrimSpace(collected.Text), nil
}
