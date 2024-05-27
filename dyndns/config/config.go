package config

import (
	"fmt"
	"io/ioutil"
	"net"
	"net/http"
	"net/url"
	"strings"

	"github.com/caarlos0/env/v11"
)

type Config struct {
	AdminLogin         string   `env:"ADMIN_LOGIN"`
	Title              string   `env:"TITLE" envDefault:"TheBBCloud DynDNS"`
	LogoutUrl          string   `env:"LOGOUT_URL"`
	ClearLogInterval   int      `env:"CLEAR_LOG_INTERVAL"`
	Domains            []string `env:"DOMAINS,notEmpty" envSeparator:";"`
	ParentNS           string   `env:"PARENT_NS,notEmpty"`
	DefaultTTL         int      `env:"DEFAULT_TTL,notEmpty"`
	AllowWildcard      bool     `env:"ALLOW_WILDCARD"`
	ExternalIP         net.IP   `env:"EXTERNAL_IP"`
	ExternalIPResolver url.URL  `env:"EXTERNAL_IP_RESOLVER" envDefault:"http://icanhazip.com"`
}

// ParseEnvs parses all needed environment variables:
// DDNS_ADMIN_LOGIN: The basic auth login string in htpasswd style.
// DDNS_DOMAINS: All domains that will be handled by the dyndns server.
func ParseEnvs() (*Config, error) {
	fmt.Println("Reading environment variables")

	c := &Config{}
	err := env.ParseWithOptions(c, env.Options{Prefix: "DDNS_"})
	if err != nil {
		return c, err
	}

	err = c.validateExternalIP()
	if err != nil {
		return c, err
	}

	c.printConfig()

	return c, err
}

func (c *Config) printConfig() {
	if c.AdminLogin == "" {
		fmt.Println("No Auth! DDNS_ADMIN_LOGIN should be set")
	}

	if c.AllowWildcard {
		fmt.Println("Wildcard allowed")
	}

	fmt.Println("External IP set:", c.ExternalIP)
	fmt.Println("External IP Resolver:", c.ExternalIPResolver.String())
	fmt.Println("Domains:", c.Domains)
	fmt.Println("Parent Namespace:", c.ParentNS)
	fmt.Println("Default TTL:", c.DefaultTTL)
	fmt.Println("Web UI Title:", c.Title)

	if c.LogoutUrl != "" {
		fmt.Println("Logout URL set:", c.LogoutUrl)
	}

	if c.ClearLogInterval > 0 {
		fmt.Println("Log clear interval found:", c.ClearLogInterval, "days")
	}
}

// Parse external IP or get it yourself.
func (c *Config) validateExternalIP() error {
	if c.ExternalIP != nil {
		return nil
	}

	resp, err := http.Get(c.ExternalIPResolver.String())
	if err != nil {
		return err
	}

	defer resp.Body.Close()

	body, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		return err
	}

	ip := strings.TrimSpace(string(body))
	c.ExternalIP = net.ParseIP(ip)
	if c.ExternalIP == nil {
		return fmt.Errorf("%s is not a valid ip", ip)
	}

	return nil
}
