package cache

func buildKeyForNodeIDMapping(resourceType, name string) string {
	return resourceType + "/" + name
}
