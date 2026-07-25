package worker

import (
	"fmt"
	"strings"

	"adp/internal/config"
)

func (c *Client) resolveServiceProfile(templateCode string, source map[string]string) (map[string]string, *config.RuntimeServiceProfile, error) {
	params := cloneStringMap(source)
	name := strings.TrimSpace(params["ServiceProfile"])
	if name == "" {
		return params, nil, nil
	}
	if c.serviceCatalog == nil {
		return nil, nil, fmt.Errorf("services config is not loaded")
	}
	serviceType := strings.ToLower(strings.TrimSpace(params["ServiceType"]))
	if serviceType == "" {
		return nil, nil, fmt.Errorf("template %q requires ServiceType when ServiceProfile is set", templateCode)
	}
	profile, err := c.serviceCatalog.Resolve(name, serviceType)
	if err != nil {
		return nil, nil, err
	}
	// Worker-local values always win over task values, preventing callers from
	// redirecting a production profile to an arbitrary host or local path.
	if profile.Host != "" {
		params["Host"] = profile.Host
	}
	if profile.Port != "" {
		params["Port"] = profile.Port
	}
	if profile.User != "" {
		params["User"] = profile.User
	}
	if profile.URL != "" {
		params["URL"] = profile.URL
	}
	if profile.Process != "" {
		params["Process"] = profile.Process
		params["ProcessName"] = profile.Process
	}
	if profile.LogFile != "" {
		params["LogFile"] = profile.LogFile
	}
	return params, &profile, nil
}
