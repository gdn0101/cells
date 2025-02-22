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

package tree

import (
	"fmt"
	"path"
	"strings"
	"time"

	"github.com/pborman/uuid"

	"github.com/golang/protobuf/proto"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"

	"github.com/pydio/cells/common"
	"github.com/pydio/cells/common/utils/std"
	json "github.com/pydio/cells/x/jsonx"
)

/* This file provides helpers and shortcuts to ease development of tree.node related features.
   As a rule of thumb, never touch the tree.pb.go that is generated via proto. */

/* VARIOUS HELPERS TO MANAGE NODES */

// Clone node to avoid modifying it directly
func (node *Node) Clone() *Node {
	return proto.Clone(node).(*Node)
}

// IsLeaf checks if node is of type NodeType_LEAF or NodeType_COLLECTION
func (node *Node) IsLeaf() bool {
	return node.Type == NodeType_LEAF
}

// IsLeafInt checks if node is of type NodeType_LEAF or NodeType_COLLECTION, return as 0/1 integer (for storing)
func (node *Node) IsLeafInt() int {
	if node.Type == NodeType_LEAF {
		return 1
	}
	return 0
}

// GetModTime returns the last modification timestamp
func (node *Node) GetModTime() time.Time {
	return time.Unix(0, node.MTime*int64(time.Second))
}

// HasSource checks if node has a DataSource and Object Service metadata set
func (node *Node) HasSource() bool {
	return node.HasMetaKey(common.MetaNamespaceDatasourceName)
}

// RenewUuidIfEmpty generates a new UUID if it is currently empty or force is set to true
func (node *Node) RenewUuidIfEmpty(force bool) {
	if node.Uuid == "" || force {
		node.Uuid = uuid.New()
	}
}

/* METADATA MANAGEMENT */

// GetMeta retrieves a metadata and unmarshall it to JSON format
func (node *Node) GetMeta(namespace string, jsonStruc interface{}) error {
	metaString := node.getMetaString(namespace)
	if metaString == "" {
		return nil
	}
	return json.Unmarshal([]byte(metaString), &jsonStruc)
}

// SetMeta sets a metadata by marshalling to JSON
func (node *Node) SetMeta(namespace string, jsonMeta interface{}) (err error) {
	if node.MetaStore == nil {
		node.MetaStore = make(map[string]string)
	}
	var bytes []byte
	bytes, err = json.Marshal(jsonMeta)
	node.MetaStore[namespace] = string(bytes)
	return err
}

// GetStringMeta easily returns the string value of the MetaData for this key
// or an empty string if the MetaData for this key is not defined
func (node *Node) GetStringMeta(namespace string) string {
	var value string
	node.GetMeta(namespace, &value)
	return value
}

// HasMetaKey checks if a metaData with this key has been defined
func (node *Node) HasMetaKey(keyName string) bool {
	if node.MetaStore == nil {
		return false
	}
	_, ok := node.MetaStore[keyName]
	return ok
}

// AllMetaDeserialized unmarshall all defined metadata to JSON objects,
// skipping reserved meta (e.g. meta that have a key prefixed by "pydio:")
func (node *Node) AllMetaDeserialized(excludes map[string]struct{}) map[string]interface{} {

	if len(node.MetaStore) == 0 {
		return map[string]interface{}{}
	}
	m := make(map[string]interface{}, len(node.MetaStore))
	for k := range node.MetaStore {
		if strings.HasPrefix(k, "pydio:") {
			continue
		}
		if excludes != nil {
			if _, x := excludes[k]; x {
				continue
			}
		}
		var data interface{}
		node.GetMeta(k, &data)
		m[k] = data
	}
	return m
}

// WithoutReservedMetas returns a copy of this node, after removing all reserved meta
func (node *Node) WithoutReservedMetas() *Node {
	newNode := proto.Clone(node).(*Node)
	for k := range newNode.MetaStore {
		if strings.HasPrefix(k, "pydio:") {
			delete(newNode.MetaStore, k)
		}
	}
	return newNode
}

// LegacyMeta enrich metadata store for this node adding info for some legacy keys
func (node *Node) LegacyMeta(meta map[string]interface{}) {
	meta["uuid"] = node.Uuid
	meta["bytesize"] = node.Size
	meta["ajxp_modiftime"] = node.MTime
	meta["etag"] = node.Etag
	if _, basename := path.Split(node.Path); basename != node.GetStringMeta("name") {
		meta["text"] = node.GetStringMeta("name")
	}
}

/* LOGGING SUPPORT */
// MarshalLogObject implements custom marshalling for logs
func (node *Node) MarshalLogObject(encoder zapcore.ObjectEncoder) error {
	if node == nil {
		return nil
	}
	if node.Uuid != "" {
		encoder.AddString("Uuid", node.Uuid)
	}
	if node.Path != "" {
		encoder.AddString("Path", node.Path)
	}
	if node.Etag != "" {
		encoder.AddString("Etag", node.Etag)
	}
	if node.MTime > 0 {
		encoder.AddTime("MTime", node.GetModTime())
	}
	if node.Size > 0 {
		encoder.AddInt64("Size", node.GetSize())
	}
	if node.MetaStore != nil {
		encoder.AddReflected("MetaStore", node.MetaStore)
	}
	return nil
}

// Zap simply returns a zapcore.Field object populated with this node and with a standard key
func (node *Node) Zap(key ...string) zapcore.Field {
	if len(key) > 0 {
		return zap.Object(key[0], node)
	} else {
		return zap.Object(common.KeyNode, node)
	}
}

func (node *Node) Zaps(key ...string) []zapcore.Field {
	k := common.KeyNode
	if len(key) > 0 {
		k = key[0]
	}
	return []zapcore.Field{node.ZapUuid(), node.ZapPath(), node.Zap(k)}
}

// ZapPath simply calls zap.String() with NodePath standard key and this node path
func (node *Node) ZapPath() zapcore.Field {
	return zap.String(common.KeyNodePath, node.GetPath())
}

// ZapUuid simply calls zap.String() with NodeUuid standard key and this node uuid
func (node *Node) ZapUuid() zapcore.Field {
	return zap.String(common.KeyNodeUuid, node.GetUuid())
}

// MarshalLogObject implements custom marshalling for logs
func (log *ChangeLog) MarshalLogObject(encoder zapcore.ObjectEncoder) error {
	if log == nil {
		return nil
	}
	if log.Uuid != "" {
		encoder.AddString("Uuid", log.Uuid)
	}
	if log.Description != "" {
		encoder.AddString("Description", log.Description)
	}
	if log.OwnerUuid != "" {
		encoder.AddString("OwnerUuid", log.OwnerUuid)
	}
	if log.MTime > 0 {
		encoder.AddTime("MTime", time.Unix(log.MTime, 0))
	}
	if log.Size > 0 {
		encoder.AddInt64("Size", log.Size)
	}
	if log.Event != nil {
		encoder.AddReflected("Event", log.Event)
	}
	if log.Location != nil {
		encoder.AddReflected("Location", log.Location)
	}
	return nil
}

// Zap simply returns a zapcore.Field object populated with this ChangeLog uneder a standard key
func (log *ChangeLog) Zap() zapcore.Field {
	return zap.Object(common.KeyChangeLog, log)
}

// MarshalLogObject implements custom marshalling for logs
func (policy *VersioningPolicy) MarshalLogObject(encoder zapcore.ObjectEncoder) error {
	if policy == nil {
		return nil
	}
	if policy.Uuid != "" {
		encoder.AddString("Uuid", policy.Uuid)
	}
	if policy.Name != "" {
		encoder.AddString("Name", policy.Name)
	}
	if policy.Description != "" {
		encoder.AddString("Description", policy.Description)
	}
	if policy.VersionsDataSourceName != "" {
		encoder.AddString("VersionsDataSourceName", policy.VersionsDataSourceName)
	}
	if policy.VersionsDataSourceBucket != "" {
		encoder.AddString("VersionsDataSourceBucket", policy.VersionsDataSourceBucket)
	}
	if policy.IgnoreFilesGreaterThan > 0 {
		encoder.AddInt64("IgnoreFilesGreaterThan", policy.IgnoreFilesGreaterThan)
	}
	if policy.MaxSizePerFile > 0 {
		encoder.AddInt64("MaxSizePerFile", policy.MaxSizePerFile)
	}
	if policy.MaxTotalSize > 0 {
		encoder.AddInt64("MaxTotalSize", policy.MaxTotalSize)
	}
	if len(policy.KeepPeriods) > 0 {
		encoder.AddReflected("Periods", policy.KeepPeriods)
	}
	return nil
}

// Zap simply returns a zapcore.Field object populated with this VersioningPolicy under a standard key
func (policy *VersioningPolicy) Zap() zapcore.Field {
	return zap.Object(common.KeyVersioningPolicy, policy)
}

// Zap simply returns a zapcore.Field object populated with this NodeChangeEvent under a standard key
func (msg *NodeChangeEvent) Zap() zapcore.Field {
	return zap.Any(common.KeyNodeChangeEvent, msg)
}

/*PACKAGE PROTECTED METHODS */

// setMetaString sets a metadata in string format
func (node *Node) setMetaString(namespace string, meta string) {
	if node.MetaStore == nil {
		node.MetaStore = make(map[string]string)
	}
	if meta == "" {
		delete(node.MetaStore, namespace)
	} else {
		node.MetaStore[namespace] = meta
	}
}

// getMetaString gets a metadata string
func (node *Node) getMetaString(namespace string) (meta string) {
	if node.MetaStore == nil {
		return ""
	}
	var ok bool
	if meta, ok = node.MetaStore[namespace]; ok {
		return meta
	}
	return ""
}

// ParseDurationDate transforms DurationDate field to proper MinDate/MaxDate values
// variadic ref is passed mostly for test, should normally use NOW for reference time
func (m *Query) ParseDurationDate(ref ...time.Time) error {
	if m.DurationDate == "" {
		return nil
	}

	firstChar := m.DurationDate[0:1]
	if firstChar != "<" && firstChar != ">" {
		return fmt.Errorf("DurationDate must start with < or > character")
	}
	ds := strings.TrimSpace(m.DurationDate[1:])
	var d time.Duration
	d, e := std.ParseCellsDuration(ds)
	if e != nil {
		return e
	}
	now := time.Now()
	if len(ref) > 0 {
		now = ref[0]
	}
	if firstChar == "<" {
		m.MinDate = now.Add(-d).Unix()
	} else {
		m.MaxDate = now.Add(-d).Unix()
	}
	return nil
}
