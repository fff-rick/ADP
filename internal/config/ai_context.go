package config

import (
	"fmt"
	"os"
	"strings"

	"gopkg.in/yaml.v3"
)

// AIContext holds service environment configuration injected into LLM prompts.
type AIContext struct {
	Services []ServiceProfile `yaml:"services"`

	// Defaults keeps compatibility with the previous AI context YAML shape.
	Defaults legacyDefaults `yaml:"defaults,omitempty"`
}

// ServiceProfile is the reusable shape for any service ADP knows about.
type ServiceProfile struct {
	Name        string            `yaml:"name"`
	Type        string            `yaml:"type"`
	Host        string            `yaml:"host,omitempty"`
	Port        string            `yaml:"port,omitempty"`
	User        string            `yaml:"user,omitempty"`
	PasswordRef string            `yaml:"password_ref,omitempty"`
	Logs        []PathProfile     `yaml:"logs,omitempty"`
	Configs     []PathProfile     `yaml:"configs,omitempty"`
	Extra       map[string]string `yaml:"extra,omitempty"`
}

// PathProfile describes a named path owned by a service.
type PathProfile struct {
	Name string `yaml:"name"`
	Path string `yaml:"path"`
}

type legacyDefaults struct {
	MySQL struct {
		Host string `yaml:"host"`
		Port string `yaml:"port"`
		User string `yaml:"user"`
	} `yaml:"mysql"`
	Redis struct {
		Host    string `yaml:"host"`
		LogPath string `yaml:"log_path"`
	} `yaml:"redis"`
	Nginx struct {
		LogPath    string `yaml:"log_path"`
		ConfigPath string `yaml:"config_path"`
	} `yaml:"nginx"`
}

// LoadAIContext loads AI context from a YAML file.
func LoadAIContext(path string) (*AIContext, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read ai context file: %w", err)
	}
	var ctx AIContext
	if err := yaml.Unmarshal(data, &ctx); err != nil {
		return nil, fmt.Errorf("parse ai context: %w", err)
	}
	ctx.Normalize()
	return &ctx, nil
}

// Normalize trims values and converts legacy defaults into service profiles.
func (c *AIContext) Normalize() {
	if len(c.Services) == 0 {
		c.Services = c.legacyServices()
	}
	for i := range c.Services {
		c.Services[i].Name = strings.TrimSpace(c.Services[i].Name)
		c.Services[i].Type = strings.ToLower(strings.TrimSpace(c.Services[i].Type))
		c.Services[i].Host = strings.TrimSpace(c.Services[i].Host)
		c.Services[i].Port = strings.TrimSpace(c.Services[i].Port)
		c.Services[i].User = strings.TrimSpace(c.Services[i].User)
		c.Services[i].PasswordRef = strings.TrimSpace(c.Services[i].PasswordRef)
	}
}

func (c *AIContext) legacyServices() []ServiceProfile {
	var services []ServiceProfile
	if c.Defaults.MySQL.Host != "" || c.Defaults.MySQL.Port != "" || c.Defaults.MySQL.User != "" {
		services = append(services, ServiceProfile{
			Name: "mysql-default",
			Type: "mysql",
			Host: c.Defaults.MySQL.Host,
			Port: c.Defaults.MySQL.Port,
			User: c.Defaults.MySQL.User,
		})
	}
	if c.Defaults.Redis.Host != "" || c.Defaults.Redis.LogPath != "" {
		services = append(services, ServiceProfile{
			Name: "redis-default",
			Type: "redis",
			Host: c.Defaults.Redis.Host,
			Logs: []PathProfile{{Name: "server", Path: c.Defaults.Redis.LogPath}},
		})
	}
	if c.Defaults.Nginx.LogPath != "" || c.Defaults.Nginx.ConfigPath != "" {
		services = append(services, ServiceProfile{
			Name:    "nginx-default",
			Type:    "nginx",
			Logs:    []PathProfile{{Name: "error", Path: c.Defaults.Nginx.LogPath}},
			Configs: []PathProfile{{Name: "main", Path: c.Defaults.Nginx.ConfigPath}},
		})
	}
	return services
}

// ToPromptSection converts the AI context into a prompt section.
func (c *AIContext) ToPromptSection() string {
	var sb strings.Builder
	sb.WriteString("## 你的环境配置（请严格使用这些值，不要编造参数）\n")
	for _, service := range c.Services {
		if service.Type == "" && service.Name == "" {
			continue
		}
		label := service.Type
		if service.Name != "" {
			label = service.Name + " (" + service.Type + ")"
		}
		sb.WriteString("- ")
		sb.WriteString(label)
		writePromptField(&sb, "host", service.Host)
		writePromptField(&sb, "port", service.Port)
		writePromptField(&sb, "user", service.User)
		writePromptField(&sb, "password_ref", service.PasswordRef)
		writePromptPaths(&sb, "logs", service.Logs)
		writePromptPaths(&sb, "configs", service.Configs)
		writePromptExtra(&sb, service.Extra)
		sb.WriteString("\n")
	}
	sb.WriteString("\n")
	return sb.String()
}

func writePromptField(sb *strings.Builder, key, value string) {
	if value == "" {
		return
	}
	sb.WriteString(", ")
	sb.WriteString(key)
	sb.WriteString("=")
	sb.WriteString(value)
}

func writePromptPaths(sb *strings.Builder, key string, paths []PathProfile) {
	if len(paths) == 0 {
		return
	}
	values := make([]string, 0, len(paths))
	for _, item := range paths {
		if item.Path == "" {
			continue
		}
		name := strings.TrimSpace(item.Name)
		if name == "" {
			name = "default"
		}
		values = append(values, name+":"+item.Path)
	}
	if len(values) > 0 {
		sb.WriteString(", ")
		sb.WriteString(key)
		sb.WriteString("=[")
		sb.WriteString(strings.Join(values, ", "))
		sb.WriteString("]")
	}
}

func writePromptExtra(sb *strings.Builder, extra map[string]string) {
	for key, value := range extra {
		if strings.TrimSpace(key) == "" || strings.TrimSpace(value) == "" {
			continue
		}
		sb.WriteString(", ")
		sb.WriteString(key)
		sb.WriteString("=")
		sb.WriteString(value)
	}
}

// FillDefaults fills in missing parameters from AI context defaults.
func (c *AIContext) FillDefaults(params map[string]string, moduleCode string) {
	if params == nil {
		return
	}
	_ = moduleCode // Template-specific defaults belong in managed template/rule YAML.
	service := c.FirstService(strings.ToLower(strings.TrimSpace(params["ServiceType"])))
	fillParam(params, "Host", service.Host)
	fillParam(params, "Port", service.Port)
	fillParam(params, "User", service.User)
	fillParam(params, "ProcessName", service.Extra["process"])
	fillParam(params, "Process", service.Extra["process"])
	fillParam(params, "LogFile", firstPath(service.Logs))
	if params["URL"] == "" && service.Host != "" {
		port := service.Port
		if port == "" {
			port = "80"
		}
		params["URL"] = "http://" + service.Host + ":" + port
	}
}

// FirstService returns the first matching service by type. Empty type returns an empty profile.
func (c *AIContext) FirstService(serviceType string) ServiceProfile {
	serviceType = strings.ToLower(strings.TrimSpace(serviceType))
	if serviceType == "" {
		return ServiceProfile{}
	}
	for _, service := range c.Services {
		if service.Type == serviceType {
			return service
		}
	}
	return ServiceProfile{}
}

func fillParam(params map[string]string, key, value string) {
	if params[key] == "" && value != "" {
		params[key] = value
	}
}

func firstPath(paths []PathProfile) string {
	for _, item := range paths {
		if strings.TrimSpace(item.Path) != "" {
			return item.Path
		}
	}
	return ""
}
