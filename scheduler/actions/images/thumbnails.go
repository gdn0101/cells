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

package images

import (
	"bytes"
	"context"
	"fmt"
	"image"
	"io"
	"io/ioutil"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"

	"github.com/disintegration/imaging"
	"github.com/golang/protobuf/proto"
	"github.com/micro/go-micro/client"
	"github.com/pkg/errors"
	"github.com/pydio/minio-go"
	"go.uber.org/zap"
	"golang.org/x/image/colornames"

	"github.com/pydio/cells/common"
	"github.com/pydio/cells/common/forms"
	"github.com/pydio/cells/common/log"
	"github.com/pydio/cells/common/proto/jobs"
	"github.com/pydio/cells/common/proto/tree"
	context2 "github.com/pydio/cells/common/utils/context"
	"github.com/pydio/cells/common/views"
	"github.com/pydio/cells/common/views/models"
	"github.com/pydio/cells/scheduler/actions"
	json "github.com/pydio/cells/x/jsonx"
)

const (
	MetadataThumbnails      = "ImageThumbnails"
	MetadataImageDimensions = "ImageDimensions"

	MetadataCompatIsImage                 = "is_image"
	MetadataCompatImageWidth              = "image_width"
	MetadataCompatImageHeight             = "image_height"
	MetadataCompatImageReadableDimensions = "readable_dimension"
)

var (
	thumbnailsActionName = "actions.images.thumbnails"
)

type ThumbnailData struct {
	Format string `json:"format"`
	Size   int    `json:"size"`
	Id     string `json:"id"`
}

type ThumbnailsMeta struct {
	Processing bool
	Thumbnails []ThumbnailData `json:"thumbnails"`
}

type ThumbnailExtractor struct {
	//Router     views.Handler
	thumbSizes map[string]int
	metaClient tree.NodeReceiverClient
	Client     client.Client
}

func (t *ThumbnailExtractor) GetDescription(_ ...string) actions.ActionDescription {
	return actions.ActionDescription{
		ID:                thumbnailsActionName,
		Label:             "Create Thumbs",
		Icon:              "image-filter",
		Description:       "Create thumbnails on image creation/modification",
		SummaryTemplate:   "",
		HasForm:           true,
		Category:          actions.ActionCategoryContents,
		InputDescription:  "Single-selection of file. Temporary and zero-bytes will be ignored",
		OutputDescription: "Input file with updated metadata",
	}
}

func (t *ThumbnailExtractor) GetParametersForm() *forms.Form {
	return &forms.Form{Groups: []*forms.Group{
		{
			Fields: []forms.Field{
				&forms.FormField{
					Name:        "ThumbSizes",
					Type:        forms.ParamTextarea,
					Label:       "Sizes",
					Description: "A JSON map describing each thumbnail to be created",
					Default:     "",
					Mandatory:   false,
					Editable:    true,
				},
			},
		},
	}}
}

// GetName returns this action unique identifier.
func (t *ThumbnailExtractor) GetName() string {
	return thumbnailsActionName
}

// Init passes parameters to the action.
func (t *ThumbnailExtractor) Init(_ *jobs.Job, cl client.Client, action *jobs.Action) error {
	if action.Parameters != nil {
		t.thumbSizes = make(map[string]int)
		if params, ok := action.Parameters["ThumbSizes"]; ok {
			if e := json.Unmarshal([]byte(params), &t.thumbSizes); e != nil {
				for i, s := range strings.Split(params, ",") {
					parsed, _ := strconv.ParseInt(s, 10, 32)
					t.thumbSizes[fmt.Sprintf("%d", i)] = int(parsed)
				}
			}
		}
	} else {
		t.thumbSizes = map[string]int{"sm": 300}
	}
	t.metaClient = tree.NewNodeReceiverClient(common.ServiceGrpcNamespace_+common.ServiceMeta, cl)
	t.Client = cl
	return nil
}

// Run the actual action code.
func (t *ThumbnailExtractor) Run(ctx context.Context, _ *actions.RunnableChannels, input jobs.ActionMessage) (jobs.ActionMessage, error) {

	if len(input.Nodes) == 0 || input.Nodes[0].Size == -1 || input.Nodes[0].Etag == common.NodeFlagEtagTemporary {
		// Nothing to do
		log.Logger(ctx).Debug("[THUMB EXTRACTOR] task ignored")
		return input.WithIgnore(), nil
	}

	log.Logger(ctx).Debug("[THUMB EXTRACTOR] Resizing image...")
	node := input.Nodes[0]
	err := t.resize(ctx, node, t.thumbSizes)
	if err != nil {
		return input.WithError(err), err
	}

	output := input
	output.Nodes[0] = node
	log.TasksLogger(ctx).Info("Created thumbnails for "+node.GetPath(), node.ZapPath())
	output.AppendOutput(&jobs.ActionOutput{Success: true})

	return output, nil
}

func displayMemStat(_ context.Context, _ string) {
	//var m runtime.MemStats
	//runtime.ReadMemStats(&m)
	//log.Logger(ctx).Info("---- MEMORY USAGE AT: "+position, zap.Uint64("Alloc", m.Alloc/1024/1024), zap.Uint64("TotalAlloc", m.TotalAlloc/1024/1024), zap.Uint64("Sys", m.Sys/1024/1024), zap.Uint32("NumGC", m.NumGC))
	//stdlog.Printf("%s : \nAlloc = %v\nTotalAlloc = %v\nSys = %v\nNumGC = %v\n\n", position, m.Alloc / 1024 / 1024, m.TotalAlloc / 1024 / 1024, m.Sys / 1024, m.NumGC)
}

func (t *ThumbnailExtractor) resize(ctx context.Context, node *tree.Node, sizes map[string]int) error {
	displayMemStat(ctx, "START RESIZE")
	// Open the test image.
	if !node.HasSource() {
		return fmt.Errorf("node does not have enough metadata for Resize (missing Source data)")
	}

	log.Logger(ctx).Debug("[THUMB EXTRACTOR] Getting object content", zap.String("Path", node.Path), zap.Int64("Size", node.Size))
	var reader io.ReadCloser
	var err error
	var errPath string

	if localPath := getNodeLocalPath(node); len(localPath) > 0 {
		reader, err = os.Open(localPath)
		errPath = localPath
	} else {
		// TODO : tmp security until Router is transmitting nodes immutably
		routerNode := proto.Clone(node).(*tree.Node)
		reader, err = getRouter().GetObject(ctx, routerNode, &models.GetRequestData{Length: -1})
		errPath = routerNode.Path
	}
	if err != nil {
		return errors.Wrap(err, errPath)
	}
	defer reader.Close()

	displayMemStat(ctx, "BEFORE DECODE")
	src, err := imaging.Decode(reader)
	if err != nil {
		return errors.Wrap(err, errPath)
	}
	displayMemStat(ctx, "AFTER DECODE")

	// Extract dimensions
	bounds := src.Bounds()
	width := bounds.Max.X
	height := bounds.Max.Y
	// Send update event right now
	node.SetMeta(MetadataImageDimensions, struct {
		Width  int
		Height int
	}{
		Width:  width,
		Height: height,
	})
	node.SetMeta(MetadataCompatIsImage, true)
	node.SetMeta(MetadataThumbnails, &ThumbnailsMeta{Processing: true})
	node.SetMeta(MetadataCompatImageHeight, height)
	node.SetMeta(MetadataCompatImageWidth, width)
	node.SetMeta(MetadataCompatImageReadableDimensions, fmt.Sprintf("%dpx X %dpx", width, height))

	if _, err = t.metaClient.UpdateNode(ctx, &tree.UpdateNodeRequest{From: node, To: node}); err != nil {
		return errors.Wrap(err, errPath)
	}

	log.Logger(ctx).Debug("Thumbnails - Extracted dimension and saved in metadata", zap.Any("dimension", bounds))
	meta := &ThumbnailsMeta{}

	for metaId, size := range sizes {

		if (metaId == "md" || metaId == "lg") && (width <= size && height <= size) {
			log.Logger(ctx).Debug("Ignoring thumbnails for size as original is smaller", zap.Any("dimension", bounds), zap.Any("thumbSize", size))
			continue
		}

		displayMemStat(ctx, "BEFORE WRITE SIZE FROM SRC")
		updateMeta, err := t.writeSizeFromSrc(ctx, src, node, size)
		if err != nil {
			// Remove processing state from Metadata
			node.SetMeta(MetadataThumbnails, nil)
			t.metaClient.UpdateNode(ctx, &tree.UpdateNodeRequest{From: node, To: node})
			return errors.Wrap(err, errPath)
		}
		displayMemStat(ctx, "AFTER WRITE SIZE FROM SRC")
		if updateMeta {
			meta.Thumbnails = append(meta.Thumbnails, ThumbnailData{
				Id:     metaId,
				Format: "jpg",
				Size:   size,
			})
		}
	}

	src = nil
	runtime.GC()

	displayMemStat(ctx, "AFTER TRIGGERING GC")

	if (meta != &ThumbnailsMeta{}) {
		node.SetMeta(MetadataThumbnails, meta)
	} else {
		node.SetMeta(MetadataThumbnails, nil)
	}

	log.Logger(ctx).Info("Thumbs Generated for", zap.String(common.KeyNodePath, errPath), zap.Any("meta", meta))
	_, err = t.metaClient.UpdateNode(ctx, &tree.UpdateNodeRequest{From: node, To: node})
	if err != nil {
		err = errors.Wrap(err, errPath)
	}

	return err
}

func (t *ThumbnailExtractor) writeSizeFromSrc(ctx context.Context, img image.Image, node *tree.Node, targetSize int) (bool, error) {

	localTest := false
	localFolder := ""

	var thumbsClient *minio.Core
	var thumbsBucket string
	objectName := fmt.Sprintf("%s-%d.jpg", node.Uuid, targetSize)

	if localFolder = node.GetStringMeta(common.MetaNamespaceNodeTestLocalFolder); localFolder != "" {
		localTest = true
	}
	logger := log.Logger(ctx)

	if !localTest {

		var e error
		thumbsClient, thumbsBucket, e = views.GetGenericStoreClient(ctx, common.PydioThumbstoreNamespace, t.Client)
		if e != nil {
			logger.Error("Cannot find client for thumbstore", zap.Error(e))
			return false, e
		}

		opts := minio.StatObjectOptions{}
		if meta, mOk := context2.MinioMetaFromContext(ctx); mOk {
			for k, v := range meta {
				opts.Set(k, v)
			}
		}
		// First Check if thumb already exists with same original etag
		oi, check := thumbsClient.StatObject(thumbsBucket, objectName, opts)
		logger.Debug("Object Info", zap.Any("object", oi), zap.Error(check))
		if check == nil {
			foundOriginal := oi.Metadata.Get("X-Amz-Meta-Original-Etag")
			if len(foundOriginal) > 0 && foundOriginal == node.Etag {
				// No update necessary
				logger.Debug("Ignoring Resize: thumb already exists in store", zap.Any("original", oi))
				return true, nil
			}
		}

	}

	logger.Debug("WriteSizeFromSrc", zap.String("nodeUuid", node.Uuid))
	var dst *image.NRGBA
	if img.Bounds().Max.X >= img.Bounds().Max.Y {
		// Resize the cropped image to width = 256px preserving the aspect ratio.
		dst = imaging.Resize(img, targetSize, 0, imaging.Lanczos)
	} else {
		// Resize the cropped image to height = 256px preserving the aspect ratio.
		dst = imaging.Resize(img, 0, targetSize, imaging.Lanczos)
	}
	ol := imaging.New(dst.Bounds().Dx(), dst.Bounds().Dy(), colornames.Lightgrey)
	ol = imaging.Overlay(ol, dst, image.Pt(0, 0), 1.0)
	dst = nil
	runtime.GC()

	displayMemStat(ctx, "BEFORE ENCODE")
	var thumbBytes []byte
	buf := bytes.NewBuffer(thumbBytes)
	err := imaging.Encode(buf, ol, imaging.JPEG)
	ol = nil
	runtime.GC()
	if err != nil {
		return false, err
	}

	displayMemStat(ctx, "AFTER ENCODE")

	if !localTest {

		requestMeta := map[string]string{
			common.XContentType:        "image/jpeg",
			"X-Amz-Meta-Original-Etag": node.Etag,
		}
		logger.Debug("Writing thumbnail to thumbs bucket", zap.Any("image size", targetSize))
		displayMemStat(ctx, "BEFORE PUT OBJECT WITH CONTEXT")
		tCtx, tNode, e := getThumbLocation(ctx, objectName)
		if e != nil {
			return false, e
		}
		tNode.Size = int64(buf.Len())
		written, err := getRouter().PutObject(tCtx, tNode, buf, &models.PutRequestData{
			Size:     tNode.Size,
			Metadata: requestMeta,
		})
		if err != nil {
			return false, err
		} else {
			logger.Debug("Finished putting thumb for size", zap.Int64("written", written), zap.Int("size ", targetSize))
		}
		displayMemStat(ctx, "AFTER PUT OBJECT WITH CONTEXT")

	} else {
		e := ioutil.WriteFile(filepath.Join(localFolder, objectName), buf.Bytes(), 0755)
		if e != nil {
			return false, e
		}
	}

	logger.Debug("WriteSizeFromSrc: END", zap.String("nodeUuid", node.Uuid))

	return true, err

}

func getNodeLocalPath(node *tree.Node) string {
	if localFolder := node.GetStringMeta(common.MetaNamespaceNodeTestLocalFolder); localFolder != "" {
		baseName := node.GetStringMeta("name")
		targetFileName := filepath.Join(localFolder, baseName)
		return targetFileName
	}
	return ""
}
