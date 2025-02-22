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

package rest

import (
	"context"
	"errors"
	"strings"

	json "github.com/pydio/cells/x/jsonx"

	"github.com/emicklei/go-restful"
	"go.uber.org/zap"

	"github.com/pydio/cells/common/config"
	"github.com/pydio/cells/common/log"
	"github.com/pydio/cells/common/proto/rest"
	"github.com/pydio/cells/common/service"
	"github.com/pydio/cells/common/utils/permissions"
)

/*********************
GENERIC GET/PUT CALLS
*********************/
func (s *Handler) PutConfig(req *restful.Request, resp *restful.Response) {

	ctx := req.Request.Context()
	var configuration rest.Configuration
	if err := req.ReadEntity(&configuration); err != nil {
		service.RestError500(req, resp, err)
		return
	}
	u, _ := permissions.FindUserNameInContext(ctx)
	if u == "" {
		u = "rest"
	}
	fullPath := strings.Trim(configuration.FullPath, "/")
	path := strings.Split(fullPath, "/")
	if len(path) == 0 {
		service.RestError401(req, resp, errors.New("no path given!"))
		return
	}
	if !config.IsRestEditable(fullPath) {
		service.RestError403(req, resp, errors.New("you are not allowed to edit that configuration"))
		return
	}
	var parsed map[string]interface{}
	if e := json.Unmarshal([]byte(configuration.Data), &parsed); e == nil {
		var original map[string]interface{}
		if o := config.Get(path...).Map(); o != nil && len(o) > 0 {
			original = o
			config.Del(path...)
		}
		config.Set(parsed, path...)
		if err := config.Save(u, "Setting config via API"); err != nil {
			log.Logger(ctx).Error("Put", zap.Error(err))
			service.RestError500(req, resp, err)
			// Restoring original value
			if original != nil {
				config.Set(original, path...)
			}
			return
		}
		s.logPluginEnabled(req.Request.Context(), configuration.FullPath, parsed, original)
		// Reload new data
		resp.WriteEntity(&rest.Configuration{
			FullPath: configuration.FullPath,
			Data:     config.Get(path...).String(),
		})
	} else {
		service.RestError500(req, resp, e)
	}

}

func (s *Handler) GetConfig(req *restful.Request, resp *restful.Response) {

	ctx := req.Request.Context()
	fullPath := strings.Trim(req.PathParameter("FullPath"), "/")
	log.Logger(ctx).Debug("Config.Get FullPath : " + fullPath)

	path := strings.Split(fullPath, "/")

	if !config.IsRestEditable(fullPath) {
		service.RestError403(req, resp, errors.New("you are not allowed to read that configuration via the REST API"))
		return
	}

	data := config.Get(path...).String()

	output := &rest.Configuration{
		FullPath: fullPath,
		Data:     data,
	}
	resp.WriteEntity(output)

}

func (s *Handler) logPluginEnabled(ctx context.Context, cPath string, conf map[string]interface{}, original map[string]interface{}) {
	k, o := conf["PYDIO_PLUGIN_ENABLED"]
	if !o {
		return
	}
	if original != nil {
		k1, o1 := original["PYDIO_PLUGIN_ENABLED"]
		if o1 && k1 == k {
			return
		}
	}
	status := k.(bool)
	if status {
		log.Auditer(ctx).Info("Enabling plugin " + cPath)
	} else {
		log.Auditer(ctx).Info("Disabling plugin " + cPath)
	}
}
