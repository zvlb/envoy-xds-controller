package utils

import "strings"

func GetWildcardDomain(domain string) string {
	var wildcardDomain string
	domainParts := strings.Split(domain, ".")
	for i, dp := range domainParts {
		if i == 0 {
			wildcardDomain = "*"
			continue
		}
		wildcardDomain = wildcardDomain + "." + dp
	}

	return wildcardDomain
}

func IntersectionStrings(slice1, slice2 []string) []string {
	result := []string{}
	elements := make(map[string]bool)

	for _, v := range slice1 {
		elements[v] = true
	}

	for _, v := range slice2 {
		if elements[v] {
			result = append(result, v)
			delete(elements, v)
		}
	}

	return result
}
