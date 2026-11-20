// Copyright (C) 2023-2026 Òscar Casajuana Alonso

package cmd

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/elboletaire/manga-downloader/grabber"
	"github.com/spf13/cobra"
)

func init() {
	var asJSON bool
	search := &cobra.Command{
		Use:          "search <title>",
		Short:        "Search supported English and Brazilian Portuguese sites by title",
		Args:         cobra.MinimumNArgs(1),
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			result, err := grabber.SearchSeries(strings.Join(args, " "))
			if err != nil {
				return err
			}
			for _, warning := range result.Warnings {
				fmt.Fprintln(cmd.ErrOrStderr(), warning)
			}
			if result.NeedsKey {
				fmt.Fprintln(cmd.ErrOrStderr(), "Searching ManGeek only. Set BRAVE_SEARCH_API_KEY or save a key in the desktop app settings to search other sites.")
			}
			if asJSON {
				return json.NewEncoder(cmd.OutOrStdout()).Encode(result.Matches)
			}
			if len(result.Matches) == 0 && len(result.Warnings) == 0 {
				fmt.Fprintln(cmd.ErrOrStderr(), "No matching series found.")
			}
			for _, match := range result.Matches {
				fmt.Fprintf(cmd.OutOrStdout(), "%s\t%s\n", match.Title, match.URL)
			}
			return nil
		},
	}
	search.Flags().BoolVar(&asJSON, "json", false, "Print matching links, sources, languages and thumbnails as JSON")
	rootCmd.AddCommand(search)
}
