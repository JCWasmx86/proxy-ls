package main

func xmlConfig(schemas [](map[string]interface{})) map[string]interface{} {
	return map[string]interface{}{
		"fileAssociations": schemas,
		"logs": map[string]interface{}{
			"client": true,
			"file":   "/tmp/lemminx.log",
		},
		"trace": map[string]interface{}{
			"server": "verbose",
		},
		"validation": map[string]interface{}{
			"enabled":                 true,
			"resolveExternalEntities": true,
			"schema": map[string]interface{}{
				"enabled": "always",
			},
		},
		"downloadExternalResources": map[string]interface{}{
			"enabled": true,
		},
	}
}

func yamlConfig(yamlSchemas map[string]interface{}) map[string]interface{} {
	return map[string]interface{}{
		"trace": map[string]interface{}{
			"server": "verbose",
		},
		"schemaStore": map[string]interface{}{
			"enable": true,
			"url":    "https://www.schemastore.org/api/json/catalog.json",
		},
		"validate": true,
		"schemas":  yamlSchemas,
	}
}
