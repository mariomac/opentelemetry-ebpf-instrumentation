// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

package request // import "go.opentelemetry.io/obi/pkg/appolly/app/request"

import (
	"go.opentelemetry.io/otel/attribute"

	"go.opentelemetry.io/obi/pkg/export/attributes"
	attr "go.opentelemetry.io/obi/pkg/export/attributes/names"
)

func serviceGraphValue(span *Span, name attr.Name) (string, bool) {
	switch name {
	case attr.Client:
		if span.IsClientSpan() {
			return span.Service.UID.Name, true
		}
		if span.PeerRoute != "" {
			return "", true
		}
	case attr.Server:
		if !span.IsClientSpan() {
			return span.Service.UID.Name, true
		}
		if span.HostRoute != "" {
			return "", true
		}
	case attr.ClientRoute:
		return span.PeerRoute, true
	case attr.ServerRoute:
		return span.HostRoute, true
	}
	return "", false
}

func ServiceGraphOTELGetters(unresolved UnresolvedNames) attributes.NamedGetters[*Span, attribute.KeyValue] {
	base := SpanOTELGetters(unresolved)
	return func(name attr.Name) (attributes.Getter[*Span, attribute.KeyValue], bool) {
		if name == attr.Client || name == attr.Server || name == attr.ClientRoute || name == attr.ServerRoute {
			getter, _ := base(name)
			return func(span *Span) attribute.KeyValue {
				if value, handled := serviceGraphValue(span, name); handled {
					if value == "" && (name == attr.ClientRoute || name == attr.ServerRoute) {
						return attribute.KeyValue{}
					}
					return name.OTEL().String(value)
				}
				return getter(span)
			}, true
		}
		return base(name)
	}
}

func ServiceGraphPromGetters(unresolved UnresolvedNames) attributes.NamedGetters[*Span, string] {
	base := SpanPromGetters(unresolved)
	return func(name attr.Name) (attributes.Getter[*Span, string], bool) {
		if name == attr.Client || name == attr.Server || name == attr.ClientRoute || name == attr.ServerRoute {
			getter, _ := base(name)
			return func(span *Span) string {
				if value, handled := serviceGraphValue(span, name); handled {
					return value
				}
				return getter(span)
			}, true
		}
		return base(name)
	}
}
