package main

import (
	"bytes"
	"io"
	"strings"

	"github.com/spf13/cobra"
)

// Cobra 1.9's bash script strips everything after the first colon from its
// protocol response. jq projections contain colons, so strip only the final
// directive line instead. Use this for both generated and installed scripts.
func generateBashCompletion(root *cobra.Command, w io.Writer) error {
	var script bytes.Buffer
	if err := root.GenBashCompletionV2(&script, true); err != nil {
		return err
	}
	_, err := io.WriteString(w, strings.ReplaceAll(script.String(), "out=${out%%:*}", "out=${out%$'\\n':*}"))
	return err
}
