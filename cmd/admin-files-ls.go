/*
 * Copyright (c) 2018. Abstrium SAS <team (at) pydio.com>
 * This file is part of Pydio Cells.
 *
 * Pydio Cells is free software: you can redistribute it and/or modify
 * it under the terms of the GNU Affero General Public License as published by
 * the Free Software Foundation, either version 3 of the License, or
 * (at your option) any later version.
 *
 * Pydio Cells is distributed in the hope that it will be useful,
 * but WITHOUT ANY WARRANTY; without even the implied warranty of
 * MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE.  See the
 * GNU Affero General Public License for more details.
 *
 * You should have received a copy of the GNU Affero General Public License
 * along with Pydio Cells.  If not, see <http://www.gnu.org/licenses/>.
 *
 * The latest code can be found at <https://pydio.com>.
 */

package cmd

import (
	"context"
	"path"
	"strings"
	"time"

	"github.com/manifoldco/promptui"

	"github.com/dustin/go-humanize"

	"github.com/olekukonko/tablewriter"

	"github.com/spf13/cobra"

	"github.com/pydio/cells/common"
	defaults "github.com/pydio/cells/common/micro"
	"github.com/pydio/cells/common/proto/tree"
	// service "github.com/pydio/cells/common/service/proto"
)

var (
	lsPath       string
	lsRecursive  bool
	lsShowHidden bool
	lsShowUuid   bool
)

var lsCmd = &cobra.Command{
	Use:   "ls",
	Short: "List files",
	Long: `
DESCRIPTION
  
  List Nodes by querying the tree microservice.

 `,
	RunE: func(cmd *cobra.Command, args []string) error {
		client := tree.NewNodeProviderClient(common.ServiceGrpcNamespace_+common.ServiceTree, defaults.NewClient())

		// List all children and move them all
		streamer, err := client.ListNodes(context.Background(), &tree.ListNodesRequest{Node: &tree.Node{Path: lsPath}, Recursive: lsRecursive})
		if err != nil {
			return err
		}

		cmd.Println("")
		cmd.Println("Listing nodes under " + promptui.Styler(promptui.FGUnderline)(lsPath))
		table := tablewriter.NewWriter(cmd.OutOrStdout())
		hh := []string{"Type", "Path", "Size", "Modified"}
		if lsShowUuid {
			hh = []string{"Type", "Path", "Uuid", "Size", "Modified"}
		}
		table.SetHeader(hh)
		res := 0
		defer streamer.Close()
		for {
			resp, err := streamer.Recv()
			if err != nil {
				break
			}
			res++
			node := resp.GetNode()
			if path.Base(node.GetPath()) == common.PydioSyncHiddenFile && !lsShowHidden {
				continue
			}
			var t, p, s, m string
			p = strings.TrimLeft(strings.TrimPrefix(node.GetPath(), lsPath), "/")
			t = "Folder"
			s = humanize.Bytes(uint64(node.GetSize()))
			if node.GetSize() == 0 {
				s = "-"
			}
			m = time.Unix(node.GetMTime(), 0).Format("02 Jan 06 15:04")
			if node.GetMTime() == 0 {
				m = "-"
			}
			if node.IsLeaf() {
				t = "File"
			}
			if lsShowUuid {
				table.Append([]string{t, p, node.GetUuid(), s, m})
			} else {
				table.Append([]string{t, p, s, m})
			}
		}
		if res > 0 {
			table.Render()
		} else {
			cmd.Println("No results")
		}
		return nil
	},
}

func init() {
	lsCmd.Flags().StringVarP(&lsPath, "path", "p", "/", "List nodes under given path")
	lsCmd.Flags().BoolVarP(&lsRecursive, "recursive", "", false, "List nodes recursively")
	lsCmd.Flags().BoolVarP(&lsShowUuid, "uuid", "", false, "Show UUIDs")
	lsCmd.Flags().BoolVarP(&lsShowHidden, "hidden", "", false, "Show hidden files (.pydio)")
	FilesCmd.AddCommand(lsCmd)
}
