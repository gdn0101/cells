/*
 * Copyright (c) 2018-2021. Abstrium SAS <team (at) pydio.com>
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

package permissions

import (
	"time"

	"github.com/micro/go-micro/broker"
	"github.com/micro/protobuf/proto"
	"github.com/patrickmn/go-cache"

	"github.com/pydio/cells/common"
	"github.com/pydio/cells/common/proto/idm"
)

var (
	aclCache *cache.Cache
)

func initAclCache() {
	aclCache = cache.New(500*time.Millisecond, 30*time.Second)
	broker.Subscribe(common.TopicIdmEvent, func(publication broker.Publication) error {
		event := &idm.ChangeEvent{}
		if e := proto.Unmarshal(publication.Message().Body, event); e != nil {
			//fmt.Println("Cannot unmarshall")
			return e
		}
		switch event.Type {
		case idm.ChangeEventType_CREATE, idm.ChangeEventType_UPDATE, idm.ChangeEventType_DELETE:
			//fmt.Println("Clearing cache on IdmEvent")
			for k, _ := range aclCache.Items() {
				aclCache.Delete(k)
			}
		}
		return nil
	})
}

func getAclCache() *cache.Cache {
	if aclCache == nil {
		initAclCache()
	}
	return aclCache
}
