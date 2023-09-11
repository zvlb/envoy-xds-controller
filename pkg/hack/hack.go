package hack

import (
	"strings"
)

func FindWildcardFromRegex(reg string) string {
	// Prepare reg
	if string(reg[len(reg)-1]) == "$" {
		reg = reg[0 : len(reg)-1]
	}
	reg = strings.ReplaceAll(reg, "\\.", ".")

	levels := strings.Split(reg, ".")

	var find bool
	var wildcard string

	for _, level := range levels {
		if IsRegex(level) {
			continue
		}
		if !find {
			wildcard = "*." + level
			find = true
		} else {
			wildcard = wildcard + "." + level
		}
	}

	return wildcard
}

func GetWildcard(reg string) string {
	levels := strings.Split(reg, ".")

	var wildcard string

	for i, level := range levels {

		if i == 0 {
			wildcard = "*"
			continue
		}

		if level == "" {
			continue
		}

		if IsRegex(level) {
			continue
		}

		wildcard = wildcard + "." + level
	}

	return wildcard
}

func IsRegex(str string) bool {
	spec := "~^$?<>\\+[](){}*"

	return strings.ContainsAny(str, spec)
}

func CheckRegex(strs []string) (withRegex, withoutRegex []string) {
	for _, s := range strs {
		if IsRegex(s) {
			withRegex = append(withRegex, s)
			continue
		}
		withoutRegex = append(withoutRegex, s)
	}

	return
}

func AllStringsIsRegex(array []string) bool {
	for _, a := range array {
		if !IsRegex(a) {
			return false
		}
	}
	return true
}

func AllStringsIsNotRegex(array []string) bool {
	for _, a := range array {
		if IsRegex(a) {
			return false
		}
	}
	return true
}

func GetWildcardsForRegexDomains(array []string) map[string][]string {
	wildcardDomains := map[string][]string{}

	for _, a := range array {
		wc := GetWildcard(a)
		_, ok := wildcardDomains[wc]
		if ok {
			wildcardDomains[wc] = append(wildcardDomains[wc], a)
			continue
		}
		wildcardDomains[wc] = []string{wc}
	}

	return wildcardDomains
}
