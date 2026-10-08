// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

package request

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"go.opentelemetry.io/obi/pkg/appolly/app/svc"
	attr "go.opentelemetry.io/obi/pkg/export/attributes/names"
)

func TestServiceGraphGetters(t *testing.T) {
	for _, client := range []bool{false, true} {
		for _, routed := range []bool{false, true} {
			span := Span{
				Type: EventTypeHTTP, PeerName: "peer", HostName: "host",
				Service: svc.Attrs{UID: svc.UID{Name: "local"}},
			}
			want := map[attr.Name]string{attr.Client: "peer", attr.Server: "local", attr.ClientRoute: "", attr.ServerRoute: ""}
			if client {
				span.Type = EventTypeHTTPClient
				want[attr.Client], want[attr.Server] = "local", "host"
			}
			if routed {
				if client {
					span.HostRoute = "host.internal"
					want[attr.Server], want[attr.ServerRoute] = "", span.HostRoute
				} else {
					span.PeerRoute = "peer.internal"
					want[attr.Client], want[attr.ClientRoute] = "", span.PeerRoute
				}
			}
			for name, value := range want {
				pg, ok := ServiceGraphPromGetters(UnresolvedNames{Outgoing: "unresolved", Incoming: "unresolved"})(name)
				require.True(t, ok)
				assert.Equal(t, value, pg(&span), name)
				og, ok := ServiceGraphOTELGetters(UnresolvedNames{})(name)
				require.True(t, ok)
				kv := og(&span)
				if value == "" && (name == attr.ClientRoute || name == attr.ServerRoute) {
					assert.False(t, kv.Valid())
				} else {
					assert.Equal(t, value, kv.Value.AsString(), name)
				}
			}
			getter, ok := SpanPromGetters(UnresolvedNames{})(attr.Server)
			require.True(t, ok)
			assert.Equal(t, "host", getter(&span), "RED metrics keep their endpoint names")
		}
	}
}
