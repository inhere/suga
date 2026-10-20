package axconfig

import (
	"fmt"
	"os"
	"strings"

	gkconfig "github.com/gookit/config/v2"
)

// configValueSource reports the highest-priority source, including inherited
// provider values whose raw leaf lives in retry/timeouts or a parent provider map.
func configValueSource(key string) (ConfigSource, string) {
	if envName := directEnvOverride(key); envName != "" && strings.TrimSpace(os.Getenv(envName)) != "" {
		return SourceEnv, envName
	}
	if raw, ok := lookupRawConfig(key); ok {
		return sourceForRawValue(raw)
	}
	if raw, ok := lookupInheritedProviderConfig(key); ok {
		return sourceForRawValue(raw)
	}
	// store.body_inline_bytes is the current name of store.tool_body_inline_bytes
	// (v0.36): a value set under the old name still comes from the config file.
	if key == "store.body_inline_bytes" {
		if raw, ok := lookupRawConfig("store.tool_body_inline_bytes"); ok {
			return sourceForRawValue(raw)
		}
	}
	if strings.HasPrefix(key, "providers.") {
		parts, _, field, nested, ok := parseProviderKey(key)
		if ok && !nested && field == "api_key" {
			switch parts[1] {
			case "openai":
				return SourceEnv, "OPENAI_API_KEY"
			case "anthropic":
				return SourceEnv, "ANTHROPIC_API_KEY"
			case "deepseek":
				return SourceEnv, "DEEPSEEK_API_KEY"
			}
		}
	}
	return SourceDefault, "default"
}

func sourceForRawValue(raw any) (ConfigSource, string) {
	if text, ok := raw.(string); ok {
		if match := envReferencePattern.FindStringSubmatch(text); len(match) == 2 {
			return SourceEnv, match[1]
		}
	}
	return SourceConfig, configFileName()
}

// lookupInheritedProviderConfig finds the raw configured parent when a dynamic
// provider/model leaf is effective through inheritance rather than its own path.
func lookupInheritedProviderConfig(key string) (any, bool) {
	parts, modelName, field, nested, ok := parseProviderKey(key)
	if !ok {
		return nil, false
	}
	if nested {
		return lookupRawConfig(field)
	}
	if modelName != "" {
		model, exists := lookupRawProviderModel(parts[1], modelName)
		if exists {
			if value, exists := model[field]; exists {
				return value, true
			}
			if strings.HasPrefix(field, "price.") {
				price, _ := model["price"].(map[string]any)
				value, exists := price[strings.TrimPrefix(field, "price.")]
				return value, exists
			}
			return model, true
		}
		baseField := strings.Split(field, ".")[0]
		return lookupRawConfig("providers." + parts[1] + "." + baseField)
	}
	return nil, false
}

func lookupRawProviderModel(providerID, modelName string) (map[string]any, bool) {
	root, ok := lookupMap(gkconfig.Data(), []string{"providers", providerID})
	if !ok {
		return nil, false
	}
	entry, ok := root.(map[string]any)
	if !ok {
		return nil, false
	}
	models, ok := entry["models"].(map[string]any)
	if !ok {
		return nil, false
	}
	model, ok := models[modelName].(map[string]any)
	return model, ok
}

func directEnvOverride(key string) string {
	switch key {
	case "model":
		return "SUGA_MODEL"
	case "max_steps":
		return "SUGA_MAX_STEPS"
	case "budget.tokens":
		return "SUGA_BUDGET_TOKENS"
	case "budget.cost_usd":
		return "SUGA_BUDGET_COST_USD"
	case "budget.scope":
		return "SUGA_BUDGET_SCOPE"
	case "budget.estimate":
		return "SUGA_BUDGET_ESTIMATE"
	case "budget.estimate_factor":
		return "SUGA_BUDGET_ESTIMATE_FACTOR"
	case "budget.notice_ratio":
		return "SUGA_BUDGET_NOTICE_RATIO"
	default:
		return ""
	}
}

func lookupRawConfig(key string) (any, bool) {
	if value, ok := lookupMap(gkconfig.Data(), strings.Split(key, ".")); ok {
		return value, true
	}
	parts, modelName, field, nested, ok := parseProviderKey(key)
	if !ok {
		return nil, false
	}
	root, ok := lookupMap(gkconfig.Data(), []string{"providers", parts[1]})
	if !ok {
		return nil, false
	}
	entry, ok := root.(map[string]any)
	if !ok {
		return nil, false
	}
	if modelName != "" {
		models, ok := entry["models"].(map[string]any)
		if !ok {
			return nil, false
		}
		model, ok := models[modelName]
		if !ok {
			return nil, false
		}
		modelMap, ok := model.(map[string]any)
		if !ok {
			return nil, false
		}
		if strings.HasPrefix(field, "price.") {
			priceField := strings.TrimPrefix(field, "price.")
			price, ok := modelMap["price"].(map[string]any)
			if !ok {
				return nil, false
			}
			value, ok := price[priceField]
			return value, ok
		}
		return modelMap[field], modelMap[field] != nil
	}
	if nested {
		section, ok := entry[parts[2]].(map[string]any)
		if !ok {
			return nil, false
		}
		value, ok := section[parts[3]]
		return value, ok
	}
	value, ok := entry[field]
	return value, ok
}

func lookupMap(root map[string]any, path []string) (any, bool) {
	var current any = root
	for _, part := range path {
		fields, ok := current.(map[string]any)
		if !ok {
			return nil, false
		}
		current, ok = fields[part]
		if !ok {
			return nil, false
		}
	}
	return current, true
}

func configFileName() string {
	if files := gkconfig.Default().LoadedFiles(); len(files) > 0 {
		return files[len(files)-1]
	}
	if explicit := strings.TrimSpace(os.Getenv("SUGA_CONFIG")); explicit != "" {
		return explicit
	}
	home, err := ResolveSugaHome()
	if err != nil {
		return "config.yaml"
	}
	return home + string(os.PathSeparator) + "config.yaml"
}

func maskSecret(value any) any {
	text := strings.TrimSpace(fmt.Sprint(value))
	if text == "" || text == "<nil>" {
		return "<unset>"
	}
	return "<set>"
}
