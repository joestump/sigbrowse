package cli

import (
	"fmt"

	"github.com/joestump/msgbrowse/internal/facts"
	"github.com/spf13/cobra"
)

func newFactsCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "facts",
		Short: "Extract AI facts about each contact from their messages",
		Long: "facts sends each contact's messages to the configured chat model and stores\n" +
			"the atomic, cited facts it returns (e.g. \"Has a dog named Biscuit\"). The facts\n" +
			"appear on the conversation page. It is incremental: a per-conversation cursor\n" +
			"means re-running after an import only analyzes new messages, and facts are\n" +
			"deduplicated per contact so reprocessing never duplicates them.\n" +
			"\n" +
			"Conversations on journal.exclude_conversations are never sent to the LLM.\n" +
			"This step performs network egress to llm.base_url; point it at a local\n" +
			"endpoint (the default) to keep message content on the machine.",
		RunE: func(cmd *cobra.Command, _ []string) error {
			cfg, err := resolveConfig()
			if err != nil {
				return err
			}
			reset, err := cmd.Flags().GetBool("reset")
			if err != nil {
				return err
			}
			batch, err := cmd.Flags().GetInt("batch-size")
			if err != nil {
				return err
			}
			concurrency, err := cmd.Flags().GetInt("concurrency")
			if err != nil {
				return err
			}
			convID, err := cmd.Flags().GetInt64("conversation")
			if err != nil {
				return err
			}

			st, err := openStore(cfg)
			if err != nil {
				return err
			}
			defer st.Close()

			sum, err := facts.Run(cmd.Context(), st, newLLMClient(cfg), facts.Options{
				Model:              cfg.LLM.ChatModel,
				BatchSize:          batch,
				Concurrency:        concurrency,
				Exclude:            cfg.Journal.ExcludeConversations,
				OnlyConversationID: convID,
				Reset:              reset,
			})
			if err != nil {
				return err
			}
			_, err = fmt.Fprintf(cmd.OutOrStdout(),
				"facts: %d added from %d messages across %d conversations (%d batches) in %dms\n",
				sum.FactsAdded, sum.MessagesParsed, sum.Conversations, sum.Batches, sum.DurationMS)
			return err
		},
	}
	cmd.Flags().Bool("reset", false, "wipe all stored facts and cursors before running")
	cmd.Flags().Int("batch-size", 60, "messages per extraction call")
	cmd.Flags().Int("concurrency", 4, "conversations processed in parallel")
	cmd.Flags().Int64("conversation", 0, "limit extraction to a single conversation id")
	return cmd
}
