package main

import (
	"log"
	"os"
	"strings"

	"gopkg.in/yaml.v3"
)

type Route struct {
	Prefix      string `yaml:"prefix"`
	Upstream    string `yaml:"upstream"`
	StripPrefix bool   `yaml:"stripPrefix"`
}

type Config struct {
	Port        string
	Auth0Domain string
	Audience    string
	CORSOrigins []string
	Routes      []Route
}

func mustLoadConfig() Config {
	cfg := Config{
		Port:        getEnv("PORT", "8080"),
		Auth0Domain: mustGetEnv("AUTH0_DOMAIN"),
		Audience:    mustGetEnv("AUTH0_AUDIENCE"),
	}

	if origins := os.Getenv("CORS_ORIGINS"); origins != "" {
		for _, o := range strings.Split(origins, ",") {
			cfg.CORSOrigins = append(cfg.CORSOrigins, strings.TrimSpace(o))
		}
	}

	routesFile := getEnv("ROUTES_FILE", "/config/routes.yaml")
	data, err := os.ReadFile(routesFile)
	if err != nil {
		log.Printf("no routes file at %s, starting with no routes", routesFile)
		return cfg
	}

	var routesCfg struct {
		Routes []Route `yaml:"routes"`
	}
	if err := yaml.Unmarshal(data, &routesCfg); err != nil {
		log.Fatalf("failed to parse routes file: %v", err)
	}
	cfg.Routes = routesCfg.Routes
	log.Printf("loaded %d routes from %s", len(cfg.Routes), routesFile)
	return cfg
}

func getEnv(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func mustGetEnv(key string) string {
	v := os.Getenv(key)
	if v == "" {
		log.Fatalf("required environment variable %s is not set", key)
	}
	return v
}
