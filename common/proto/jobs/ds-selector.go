package jobs

import (
	"context"

	"github.com/golang/protobuf/ptypes"
	"github.com/micro/go-micro/client"

	"github.com/pydio/cells/common/config"
	"github.com/pydio/cells/common/proto/object"
	service "github.com/pydio/cells/common/service/proto"
)

func (m *DataSourceSelector) Filter(ctx context.Context, input ActionMessage) (ActionMessage, *ActionMessage, bool) {
	var passed, excluded []*object.DataSource
	for _, ds := range input.DataSources {
		if m.All || m.evaluate(ctx, m.Query, input, ds) {
			passed = append(passed, ds)
		} else {
			excluded = append(passed, ds)
		}
	}
	input.DataSources = passed
	var x *ActionMessage
	if len(excluded) > 0 {
		filteredOutput := input
		filteredOutput.DataSources = excluded
		x = &filteredOutput
	}
	return input, x, len(passed) > 0
}

func (m *DataSourceSelector) Select(cl client.Client, ctx context.Context, input ActionMessage, objects chan interface{}, done chan bool) error {
	defer func() {
		done <- true
	}()
	for _, ds := range m.loadDSS() {
		if m.All || m.evaluate(ctx, m.Query, input, ds) {
			objects <- ds
		}
	}
	return nil
}

func (m *DataSourceSelector) MultipleSelection() bool {
	return m.Collect
}

func (m *DataSourceSelector) loadDSS() (sources []*object.DataSource) {
	for _, ds := range config.ListSourcesFromConfig() {
		sources = append(sources, ds)
	}
	return
}

func (m *DataSourceSelector) evaluate(ctx context.Context, query *service.Query, input ActionMessage, dsObject *object.DataSource) bool {

	var bb []bool
	for _, q := range query.SubQueries {
		msg := &object.DataSourceSingleQuery{}
		subQ := &service.Query{}
		if e := ptypes.UnmarshalAny(q, msg); e == nil {
			// Evaluate fields
			msg.Name = EvaluateFieldStr(ctx, input, msg.Name)
			msg.ObjectServiceName = EvaluateFieldStr(ctx, input, msg.ObjectServiceName)
			msg.StorageConfigurationName = EvaluateFieldStr(ctx, input, msg.StorageConfigurationName)
			msg.StorageConfigurationValue = EvaluateFieldStr(ctx, input, msg.StorageConfigurationValue)
			msg.PeerAddress = EvaluateFieldStr(ctx, input, msg.PeerAddress)
			msg.EncryptionKey = EvaluateFieldStr(ctx, input, msg.EncryptionKey)
			msg.VersioningPolicyName = EvaluateFieldStr(ctx, input, msg.VersioningPolicyName)
			bb = append(bb, msg.Matches(dsObject))
		} else if e := ptypes.UnmarshalAny(q, subQ); e == nil {
			bb = append(bb, m.evaluate(ctx, subQ, input, dsObject))
		}
	}
	return service.ReduceQueryBooleans(bb, query.Operation)

}
