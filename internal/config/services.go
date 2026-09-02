package config

import (
	"bufio"
	"fmt"
	"os"
	"regexp"
	"strings"
)

const DefaultServicesConfigPath = "/etc/adp/services.cnf"

var serviceProfileName = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,63}$`)

// RuntimeServiceProfile is a Worker-local service configuration. It must not
// be sent to the server or included in job output.
type RuntimeServiceProfile struct {
	Name       string
	Type       string
	Host       string
	Port       string
	User       string
	Password   string
	URL        string
	Process    string
	Unit       string
	LogFile    string
	ConfigFile string
}

type ServiceCatalog struct {
	profiles map[string]RuntimeServiceProfile
}

func LoadServiceCatalog(path string) (*ServiceCatalog, error) {
	if strings.TrimSpace(path) == "" {
		path = DefaultServicesConfigPath
	}
	info, err := os.Stat(path)
	if err != nil {
		return nil, fmt.Errorf("stat services config: %w", err)
	}
	if !info.Mode().IsRegular() || info.Mode().Perm()&0o022 != 0 {
		return nil, fmt.Errorf("services config must be a regular file not writable by group or others: %s", path)
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open services config: %w", err)
	}
	defer file.Close() //nolint:errcheck // read-only config file cleanup

	catalog := &ServiceCatalog{profiles: make(map[string]RuntimeServiceProfile)}
	var current *RuntimeServiceProfile
	scanner := bufio.NewScanner(file)
	for lineNo := 1; scanner.Scan(); lineNo++ {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, ";") {
			continue
		}
		if strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]") {
			if current != nil {
				if err := catalog.add(*current); err != nil {
					return nil, err
				}
			}
			name := strings.TrimSpace(line[1 : len(line)-1])
			if !serviceProfileName.MatchString(name) {
				return nil, fmt.Errorf("invalid service profile name at line %d", lineNo)
			}
			current = &RuntimeServiceProfile{Name: name}
			continue
		}
		if current == nil {
			return nil, fmt.Errorf("service property outside a profile at line %d", lineNo)
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			return nil, fmt.Errorf("invalid service property at line %d", lineNo)
		}
		switch strings.TrimSpace(strings.ToLower(key)) {
		case "type":
			current.Type = strings.ToLower(strings.TrimSpace(value))
		case "host":
			current.Host = strings.TrimSpace(value)
		case "port":
			current.Port = strings.TrimSpace(value)
		case "user":
			current.User = strings.TrimSpace(value)
		case "password":
			current.Password = strings.TrimSpace(value)
		case "url":
			current.URL = strings.TrimSpace(value)
		case "process":
			current.Process = strings.TrimSpace(value)
		case "unit":
			current.Unit = strings.TrimSpace(value)
		case "log_file":
			current.LogFile = strings.TrimSpace(value)
		case "config_file":
			current.ConfigFile = strings.TrimSpace(value)
		default:
			return nil, fmt.Errorf("unsupported service property %q at line %d", strings.TrimSpace(key), lineNo)
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("read services config: %w", err)
	}
	if current != nil {
		if err := catalog.add(*current); err != nil {
			return nil, err
		}
	}
	return catalog, nil
}

func (c *ServiceCatalog) add(profile RuntimeServiceProfile) error {
	if profile.Type == "" || profile.Host == "" {
		return fmt.Errorf("service profile %q requires type and host", profile.Name)
	}
	if _, exists := c.profiles[profile.Name]; exists {
		return fmt.Errorf("duplicate service profile %q", profile.Name)
	}
	c.profiles[profile.Name] = profile
	return nil
}

func (c *ServiceCatalog) Resolve(name, serviceType string) (RuntimeServiceProfile, error) {
	if !serviceProfileName.MatchString(name) {
		return RuntimeServiceProfile{}, fmt.Errorf("invalid service profile")
	}
	profile, ok := c.profiles[name]
	if !ok {
		return RuntimeServiceProfile{}, fmt.Errorf("service profile %q not found", name)
	}
	if serviceType != "" && profile.Type != serviceType {
		return RuntimeServiceProfile{}, fmt.Errorf("service profile %q has type %q, expected %q", name, profile.Type, serviceType)
	}
	return profile, nil
}

// ResolveAuto returns the sole local profile of serviceType. It deliberately
// refuses ambiguous selection: the control plane may request convenience, but
// it must never silently choose between multiple production targets.
func (c *ServiceCatalog) ResolveAuto(serviceType string) (RuntimeServiceProfile, error) {
	serviceType = strings.ToLower(strings.TrimSpace(serviceType))
	if serviceType == "" {
		return RuntimeServiceProfile{}, fmt.Errorf("service type is required for automatic profile selection")
	}
	var matched []RuntimeServiceProfile
	for _, profile := range c.profiles {
		if profile.Type == serviceType {
			matched = append(matched, profile)
		}
	}
	if len(matched) != 1 {
		return RuntimeServiceProfile{}, fmt.Errorf("automatic profile selection for type %q requires exactly one local profile, found %d", serviceType, len(matched))
	}
	return matched[0], nil
}
