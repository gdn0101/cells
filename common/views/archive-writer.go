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

package views

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"context"
	"io"
	"path"
	"strings"

	"go.uber.org/zap"

	"github.com/pydio/cells/common/log"
	"github.com/pydio/cells/common/proto/tree"
	"github.com/pydio/cells/common/views/models"
)

type ArchiveWriter struct {
	Router Handler

	// Optional filter when listing nodes to build the archive
	WalkFilter WalkFilter
}

func (w *ArchiveWriter) commonRoot(nodes []*tree.Node) string {

	// TODO
	// Assume nodes have same parent for now
	if len(nodes) == 1 && !nodes[0].IsLeaf() {
		return nodes[0].Path
	} else {
		return path.Dir(nodes[0].Path)
	}

}

// ZipSelection creates a .zip archive from nodes selection
func (w *ArchiveWriter) ZipSelection(ctx context.Context, output io.Writer, nodes []*tree.Node, logsChannels ...chan string) (int64, error) {

	z := zip.NewWriter(output)
	defer z.Close()
	var totalSizeWritten int64

	// Make sure to load root nodes
	for i, n := range nodes {
		if r, e := w.Router.ReadNode(ctx, &tree.ReadNodeRequest{Node: n}); e != nil {
			return 0, e
		} else {
			nodes[i] = r.GetNode()
		}
	}

	parentRoot := w.commonRoot(nodes)

	log.Logger(ctx).Debug("ZipSelection", zap.String("parent", parentRoot), zap.Int("selection size", len(nodes)))

	filters := []WalkFilter{
		WalkFilterSkipPydioHiddenFile,
		func(ctx context.Context, node *tree.Node) bool {
			return node.Size > 0
		},
	}
	if w.WalkFilter != nil {
		filters = append(filters, w.WalkFilter)
	}
	for _, node := range nodes {

		request := &tree.ListNodesRequest{
			Node:       &tree.Node{Path: node.Path},
			Recursive:  true,
			Limit:      0,
			FilterType: tree.NodeType_LEAF,
		}
		err := w.Router.ListNodesWithCallback(ctx, request, func(ctx context.Context, n *tree.Node, err error) error {

			if err != nil {
				return nil
			}
			internalPath := strings.TrimPrefix(n.Path, parentRoot)
			internalPath = strings.TrimLeft(internalPath, "/")
			if internalPath == "" {
				return nil
			}
			log.Logger(ctx).Debug("Adding file to archive: ", zap.String("path", internalPath), zap.Any("node", n))
			header := &zip.FileHeader{
				Name:               internalPath,
				Method:             zip.Deflate,
				UncompressedSize64: uint64(n.Size),
			}
			header.SetMode(0777)
			header.Modified = n.GetModTime()
			r, e1 := w.Router.GetObject(ctx, n, &models.GetRequestData{StartOffset: 0, Length: -1})
			if is403(e1) {
				// IGNORE
				log.Logger(ctx).Debug("Ignoring file for archive: ", zap.String("path", internalPath), zap.Any("node", n), zap.Error(e1))
				return nil
			}
			if e1 != nil {
				log.Logger(ctx).Error("Error while getting object, will not be appended to archive", zap.String("path", n.Path), zap.Error(e1))
				return e1
			}
			defer r.Close()
			zW, e := z.CreateHeader(header)
			if e != nil {
				log.Logger(ctx).Error("Error while creating path", zap.String("path", internalPath), zap.Error(e))
				return e
			}
			written, e2 := io.Copy(zW, r)
			if e2 != nil {
				log.Logger(ctx).Error("Error while copying streams", zap.Error(e2))
				return e2
			}
			totalSizeWritten += written

			if len(logsChannels) > 0 {
				logsChannels[0] <- "File " + internalPath + " added to archive"
			}

			return nil
		}, false, filters...)

		if err != nil {
			return 0, err
		}
	}

	log.Logger(ctx).Debug("Total Size Written", zap.Int64("size", totalSizeWritten))

	return totalSizeWritten, nil
}

// TarSelection creates a .tar or .tar.gz archive from nodes selection
func (w *ArchiveWriter) TarSelection(ctx context.Context, output io.Writer, gzipFile bool, nodes []*tree.Node, logsChannel ...chan string) (int64, error) {

	var tw *tar.Writer
	var totalSizeWritten int64

	if gzipFile {
		// set up the gzip writer
		gw := gzip.NewWriter(output)
		defer gw.Close()

		tw = tar.NewWriter(gw)
		defer tw.Close()
	} else {
		tw = tar.NewWriter(output)
		defer tw.Close()
	}

	parentRoot := w.commonRoot(nodes)

	for _, node := range nodes {

		request := &tree.ListNodesRequest{
			Node:       &tree.Node{Path: node.Path},
			Recursive:  true,
			Limit:      0,
			FilterType: tree.NodeType_LEAF,
		}
		err := w.Router.ListNodesWithCallback(ctx, request, func(ctx context.Context, n *tree.Node, err error) error {

			internalPath := strings.TrimPrefix(n.Path, parentRoot)
			header := &tar.Header{
				Name:    internalPath,
				ModTime: n.GetModTime(),
				Size:    n.Size,
				Mode:    0777,
			}
			if !n.IsLeaf() {
				header.Typeflag = tar.TypeDir
			} else {
				header.Typeflag = tar.TypeReg
			}
			log.Logger(ctx).Debug("Adding file to archive: ", zap.String("path", internalPath), zap.Any("node", n))
			reader, e1 := w.Router.GetObject(ctx, n, &models.GetRequestData{StartOffset: 0, Length: -1})
			if is403(e1) {
				log.Logger(ctx).Debug("Ignore file to archive: ", zap.String("path", internalPath), zap.Any("node", n), zap.Error(e1))
				return nil
			}
			if e1 != nil {
				log.Logger(ctx).Error("Error while getting object and writing to tarball", zap.String("path", internalPath), zap.Error(e1))
				return e1
			}
			defer reader.Close()

			e := tw.WriteHeader(header)
			if e != nil {
				log.Logger(ctx).Error("Error while creating path", zap.String("path", internalPath), zap.Error(e))
				return e
			}

			size, _ := io.Copy(tw, reader)
			totalSizeWritten += size

			if len(logsChannel) > 0 {
				logsChannel[0] <- "File " + internalPath + " added to archive"
			}
			return nil

		}, true, WalkFilterSkipPydioHiddenFile, func(ctx context.Context, node *tree.Node) bool {
			return node.Size > 0
		})

		if err != nil {
			return totalSizeWritten, err
		}

	}

	return totalSizeWritten, nil

}
