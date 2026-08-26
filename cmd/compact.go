package cmd

import (
	"fmt"

	"github.com/spf13/cobra"
)

func compactCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "compact [path_to_data]",
		Short: "compact databases (rewrite by default: copy live keys into a fresh DB)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			home := args[0]
			// compact subcommand defaults to rewrite unless user passed --rewrite=false
			if !cmd.Flags().Changed("rewrite") {
				rewrite = true
			}

			if rewrite {
				fmt.Println("Using rewrite compaction (needs free disk ≈ size of each DB while copying)")
			}

			if tendermint {
				fmt.Println("compacting blockstore")
				if err := rewriteOrForceCompactComet("blockstore", home); err != nil {
					fmt.Println(err.Error())
				}
				fmt.Println("compacting state")
				if err := rewriteOrForceCompactComet("state", home); err != nil {
					fmt.Println(err.Error())
				}
			}

			if cosmosSdk {
				fmt.Println("compacting application")
				if err := rewriteOrForceCompactCosmos("application", home); err != nil {
					fmt.Println(err.Error())
				}
			}

			if tx_idx {
				fmt.Println("compacting tx_index")
				if err := rewriteOrForceCompactCosmos("tx_index", home); err != nil {
					fmt.Println(err.Error())
				}
			}

			fmt.Println("compact finished")
			return nil
		},
	}
	return cmd
}
