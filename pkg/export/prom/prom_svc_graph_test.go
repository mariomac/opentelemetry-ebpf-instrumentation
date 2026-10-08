// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

package prom

import (
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"go.opentelemetry.io/obi/pkg/appolly/app/request"
	"go.opentelemetry.io/obi/pkg/appolly/app/svc"
	"go.opentelemetry.io/obi/pkg/appolly/discover/exec"
	"go.opentelemetry.io/obi/pkg/export"
	"go.opentelemetry.io/obi/pkg/export/attributes"
	"go.opentelemetry.io/obi/pkg/export/connector"
	"go.opentelemetry.io/obi/pkg/export/instrumentations"
	"go.opentelemetry.io/obi/pkg/export/otel/perapp"
	"go.opentelemetry.io/obi/pkg/pipe/global"
	"go.opentelemetry.io/obi/pkg/pipe/msg"
)

func TestServiceGraphRoutedEndpoints(t *testing.T) {
	now := time.Unix(1000, 0)
	originalClock := timeNow
	timeNow = func() time.Time { return now }
	t.Cleanup(func() { timeNow = originalClock })
	for _, features := range []export.Features{export.FeatureGraph, export.FeatureApplicationRED} {
		registry := prometheus.NewRegistry()
		r, err := newReporter(t.Context(), &global.ContextInfo{Prometheus: &connector.PrometheusManager{}},
			&PrometheusConfig{
				Registry: registry, TTL: time.Minute, SpanMetricsServiceCacheSize: 10,
				Instrumentations: []instrumentations.Instrumentation{instrumentations.InstrumentationALL},
			},
			&perapp.GlobalMetricsConfig{Features: features}, &attributes.SelectorConfig{}, request.UnresolvedNames{},
			msg.NewQueue[[]request.Span](), msg.NewQueue[exec.ProcessEvent](), nil)
		require.NoError(t, err)
		span := request.Span{
			Type: request.EventTypeHTTPClient, HostName: "remote.internal", HostRoute: "remote.internal",
			Peer: "10.0.0.1", Host: "10.0.0.2",
			PeerName: "local.internal", LocalRoutes: []string{"local.internal", "other.internal"},
			Service:      svc.Attrs{Features: features, UID: svc.UID{Name: "local", Namespace: "apps"}},
			RequestStart: 100, End: 200, Status: 500,
		}
		r.observe(&span)
		r.observe(&span)
		endpointLabels := map[string]string{"service_name": "local", "service_namespace": "apps", "route": "local.internal", "source": "obi"}
		if !features.ServiceGraph() {
			assert.Nil(t, gatheredMetric(t, registry, ServiceGraphEndpoint, endpointLabels))
			assert.Nil(t, r.serviceGraphEndpoint)
			continue
		}
		for _, route := range span.LocalRoutes {
			endpointLabels["route"] = route
			point := gatheredMetric(t, registry, ServiceGraphEndpoint, endpointLabels)
			require.NotNil(t, point)
			assert.InDelta(t, 1, point.GetGauge().GetValue(), 0)
		}
		labels := map[string]string{
			"client": "local", "server": "", "client_service_namespace": "apps",
			"server_service_namespace": "", "client_route": "", "server_route": "remote.internal", "source": "obi", "connection_type": "virtual_node",
		}
		for _, name := range []string{ServiceGraphClient, ServiceGraphTotal, ServiceGraphFailed} {
			require.NotNil(t, gatheredMetric(t, registry, name, labels), name)
		}
		span.Type, span.HostRoute, span.PeerRoute = request.EventTypeHTTP, "", "remote.internal"
		r.observe(&span)
		labels["client"], labels["server"] = "", "local"
		labels["client_route"], labels["server_route"] = "remote.internal", ""
		labels["client_service_namespace"], labels["server_service_namespace"] = "", "apps"
		labels["connection_type"] = ""
		for _, name := range []string{ServiceGraphServer, ServiceGraphTotal, ServiceGraphFailed} {
			require.NotNil(t, gatheredMetric(t, registry, name, labels), name)
		}
		now = now.Add(2 * time.Minute)
		span.LocalRoutes = []string{"new.internal"}
		r.observe(&span)
		assert.Nil(t, gatheredMetric(t, registry, ServiceGraphEndpoint, endpointLabels))
		endpointLabels["route"] = "new.internal"
		require.NotNil(t, gatheredMetric(t, registry, ServiceGraphEndpoint, endpointLabels))
	}
}
