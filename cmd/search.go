// Copyright (C) 2023-2026 Òscar Casajuana Alonso

package cmd

import (
	"fmt"
	"strings"

	"github.com/elboletaire/manga-downloader/grabber"
	"github.com/spf13/cobra"
)

func init() {
	rootCmd.AddCommand(&cobra.Command{
		Use:          "search <title>",
		Short:        "Search ManGeek by title and print matching series links",
		Args:         cobra.MinimumNArgs(1),
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			matches, err := grabber.SearchManGeek(strings.Join(args, " "))
			if err != nil {
				return err
			}
			if len(matches) == 0 {
				fmt.Fprintln(cmd.ErrOrStderr(), "No matching series on ManGeek.")
			}
			for _, match := range matches {
				fmt.Fprintf(cmd.OutOrStdout(), "%s\t%s\n", match.Title, match.URL)
			}
			return nil
		},
	})
}
