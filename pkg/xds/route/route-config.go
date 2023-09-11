package route

import (
	"fmt"

	routev3 "github.com/envoyproxy/go-control-plane/envoy/config/route/v3"
	v32 "github.com/envoyproxy/go-control-plane/envoy/type/matcher/v3"

	"github.com/kaasops/envoy-xds-controller/pkg/hack"
)

const (
	RegexPrefix = "regex"
)

func MakeRouteConfig(vh *routev3.VirtualHost, name string) ([]*routev3.RouteConfiguration, error) {
	routeConfigs := []*routev3.RouteConfiguration{}

	domainsWithRegex, domainsWithoutRegex := hack.CheckRegex(vh.Domains)

	if len(domainsWithRegex) > 0 {
		routeForRegex := vh.Routes

		addMatchforRegexDomain(routeForRegex, domainsWithRegex)
		rc := &routev3.RouteConfiguration{
			Name: fmt.Sprintf("%s-%s", RegexPrefix, name),
			VirtualHosts: []*routev3.VirtualHost{{
				Name:                name,
				Domains:             []string{"*"},
				Routes:              vh.Routes,
				RequestHeadersToAdd: vh.RequestHeadersToAdd,
			}},
		}
		routeConfigs = append(routeConfigs, rc)
	}

	if len(domainsWithoutRegex) > 0 {
		// Replace Domains list, can make config problems!!!
		rc := &routev3.RouteConfiguration{
			Name: name,
			VirtualHosts: []*routev3.VirtualHost{{
				Name:                name,
				Domains:             []string{"*"},
				Routes:              vh.Routes,
				RequestHeadersToAdd: vh.RequestHeadersToAdd,
			}},
		}
		routeConfigs = append(routeConfigs, rc)
	}

	for _, rc := range routeConfigs {
		if err := rc.ValidateAll(); err != nil {
			return nil, err
		}
	}

	return routeConfigs, nil
}

func addMatchforRegexDomain(routes []*routev3.Route, domains []string) {
	for _, route := range routes {
		regexHeaders := []*routev3.HeaderMatcher{}
		for _, domain := range domains {
			regexHeader := &routev3.HeaderMatcher{
				HeaderMatchSpecifier: &routev3.HeaderMatcher_SafeRegexMatch{
					SafeRegexMatch: &v32.RegexMatcher{
						EngineType: &v32.RegexMatcher_GoogleRe2{},
						Regex:      domain,
					},
				},
				Name: ":authority",
			}
			regexHeaders = append(regexHeaders, regexHeader)
		}

		route.Match.Headers = append(route.Match.Headers, regexHeaders...)
	}
}
