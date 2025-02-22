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
	"context"
	"fmt"

	"github.com/micro/go-micro/client"
	"go.uber.org/zap"

	"github.com/pydio/cells/common"
	"github.com/pydio/cells/common/forms"
	"github.com/pydio/cells/common/log"
	"github.com/pydio/cells/common/proto/jobs"
	"github.com/pydio/cells/common/proto/tree"
	"github.com/pydio/cells/common/views"
	"github.com/pydio/cells/scheduler/actions"
)

var (
	cleanThumbTaskName = "actions.images.clean"
)

type CleanThumbsTask struct {
	Client client.Client
}

func (c *CleanThumbsTask) GetDescription(lang ...string) actions.ActionDescription {
	return actions.ActionDescription{
		ID:              cleanThumbTaskName,
		Label:           "Clean Thumbs",
		Icon:            "image-broken-variant",
		Category:        actions.ActionCategoryContents,
		Description:     "Remove thumbnails associated to deleted images",
		SummaryTemplate: "",
		HasForm:         false,
	}
}

func (c *CleanThumbsTask) GetParametersForm() *forms.Form {
	return nil
}

// GetName returns this action unique identifier.
func (c *CleanThumbsTask) GetName() string {
	return cleanThumbTaskName
}

// Init passes parameters to the action.
func (c *CleanThumbsTask) Init(job *jobs.Job, cl client.Client, action *jobs.Action) error {
	c.Client = cl
	return nil
}

// Run the actual action code
func (c *CleanThumbsTask) Run(ctx context.Context, channels *actions.RunnableChannels, input jobs.ActionMessage) (jobs.ActionMessage, error) {

	if len(input.Nodes) == 0 {
		return input.WithIgnore(), nil
	}

	thumbsClient, thumbsBucket, e := views.GetGenericStoreClient(ctx, common.PydioThumbstoreNamespace, c.Client)
	if e != nil {
		log.Logger(ctx).Debug("Cannot get ThumbStoreClient", zap.Error(e), zap.Any("context", ctx))
		return input.WithError(e), e
	}
	nodeUuid := input.Nodes[0].Uuid
	// List all thumbs starting with node Uuid
	listRes, err := thumbsClient.ListObjectsWithContext(ctx, thumbsBucket, nodeUuid+"-", "", "", 0)
	if err != nil {
		log.Logger(ctx).Debug("Cannot get ThumbStoreClient", zap.Error(err), zap.Any("context", ctx))
		return input.WithError(err), err
	}
	for _, oi := range listRes.Contents {
		tCtx, tNode, e := getThumbLocation(ctx, oi.Key)
		if e != nil {
			log.Logger(ctx).Debug("Cannot get thumbnail location", zap.Error(e))
			return input.WithError(e), e
		}
		if _, err := getRouter().DeleteNode(tCtx, &tree.DeleteNodeRequest{Node: tNode}); err != nil {
			log.Logger(ctx).Debug("Cannot delete thumbnail", zap.Error(err))
			return input.WithError(err), err
		}
		log.TasksLogger(ctx).Info(fmt.Sprintf("Successfully removed object %s", oi.Key))
	}
	output := jobs.ActionMessage{}
	return output, nil
}
