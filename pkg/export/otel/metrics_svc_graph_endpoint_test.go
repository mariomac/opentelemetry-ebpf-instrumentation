// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

package otel

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"

	"go.opentelemetry.io/obi/pkg/appolly/app/request"
	"go.opentelemetry.io/obi/pkg/appolly/app/svc"
	"go.opentelemetry.io/obi/pkg/export/attributes"
	attr "go.opentelemetry.io/obi/pkg/export/attributes/names"
	"go.opentelemetry.io/obi/pkg/export/otel/metric"
	"go.opentelemetry.io/obi/pkg/export/otel/otelcfg"
)

func TestServiceGraphRoutedEndpoints(t *testing.T) {
	now := time.Unix(1000, 0)
	originalClock := timeNow
	timeNow = func() time.Time { return now }
	t.Cleanup(func() { timeNow = originalClock })
	reader := metric.NewManualReader()
	provider := metric.NewMeterProvider(metric.WithReader(reader))
	t.Cleanup(func() { require.NoError(t, provider.Shutdown(t.Context())) })
	mr := &SvcGraphMetricsReporter{
		cfg:              &otelcfg.MetricsConfig{TTL: time.Minute},
		metricAttributes: serviceGraphGetters(request.UnresolvedNames{}, false), pidTracker: NewPidServiceTracker(),
	}
	r := &SvcGraphMetrics{ctx: t.Context()}
	require.NoError(t, mr.setupGraphMeters(r, provider.Meter(reporterName)))
	span := request.Span{
		Type: request.EventTypeHTTPClient, HostName: "remote.internal", HostRoute: "remote.internal",
		Peer: "10.0.0.1", Host: "10.0.0.2",
		PeerName: "local.internal", LocalRoutes: []string{"local.internal", "other.internal"},
		Service: svc.Attrs{UID: svc.UID{Name: "local", Namespace: "apps"}}, RequestStart: 100, End: 200, Status: 500,
	}
	r.record(&span, mr)
	r.record(&span, mr)
	span.Type, span.HostRoute, span.PeerRoute = request.EventTypeHTTP, "", "remote.internal"
	r.record(&span, mr)
	collect := func() map[string]metricdata.Metrics {
		t.Helper()
		var data metricdata.ResourceMetrics
		require.NoError(t, reader.Collect(t.Context(), &data))
		metrics := map[string]metricdata.Metrics{}
		for _, scope := range data.ScopeMetrics {
			for _, m := range scope.Metrics {
				metrics[m.Name] = m
			}
		}
		return metrics
	}
	metrics := collect()
	gauge, ok := metrics[attributes.ServiceGraphEndpoint.OTEL].Data.(metricdata.Gauge[int64])
	require.True(t, ok)
	require.Len(t, gauge.DataPoints, 2)
	routes := []string{}
	for _, point := range gauge.DataPoints {
		assert.EqualValues(t, 1, point.Value)
		name, _ := point.Attributes.Value(attr.ServiceName.OTEL())
		assert.Equal(t, "local", name.AsString())
		namespace, _ := point.Attributes.Value(attr.ServiceNamespace.OTEL())
		assert.Equal(t, "apps", namespace.AsString())
		route, _ := point.Attributes.Value(attr.ServiceGraphRoute.OTEL())
		routes = append(routes, route.AsString())
	}
	assert.ElementsMatch(t, span.LocalRoutes, routes)
	for _, tc := range []struct {
		name                     attributes.Name
		client, server, routeKey string
	}{
		{attributes.ServiceGraphClient, "local", "", "server.route"},
		{attributes.ServiceGraphServer, "", "local", "client.route"},
	} {
		hist, ok := metrics[tc.name.OTEL].Data.(metricdata.Histogram[float64])
		require.True(t, ok)
		require.Len(t, hist.DataPoints, 1)
		attrs := hist.DataPoints[0].Attributes
		client, _ := attrs.Value(attr.Client.OTEL())
		server, _ := attrs.Value(attr.Server.OTEL())
		route, _ := attrs.Value(attribute.Key(tc.routeKey))
		assert.Equal(t, tc.client, client.AsString())
		assert.Equal(t, tc.server, server.AsString())
		assert.Equal(t, "remote.internal", route.AsString())
	}
	for _, name := range []attributes.Name{attributes.ServiceGraphTotal, attributes.ServiceGraphFailed} {
		sum, ok := metrics[name.OTEL].Data.(metricdata.Sum[int64])
		require.True(t, ok)
		require.Len(t, sum.DataPoints, 2, "incoming and outgoing attribute sets stay distinct")
	}
	now = now.Add(2 * time.Minute)
	span.LocalRoutes = []string{"new.internal"}
	r.record(&span, mr)
	gauge = collect()[attributes.ServiceGraphEndpoint.OTEL].Data.(metricdata.Gauge[int64])
	require.Len(t, gauge.DataPoints, 1)
	route, _ := gauge.DataPoints[0].Attributes.Value(attr.ServiceGraphRoute.OTEL())
	assert.Equal(t, "new.internal", route.AsString())
	now = now.Add(2 * time.Minute)
	span.LocalRoutes = nil
	r.record(&span, mr)
	assert.NotContains(t, collect(), attributes.ServiceGraphEndpoint.OTEL, "deleted DNS records expire even without a replacement")
	r.cleanupAllMetricsInstances()
	assert.Empty(t, collect(), "reporter eviction removes endpoint mappings too")
}
