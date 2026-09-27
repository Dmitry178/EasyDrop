package models

// Config represents the exact mapping of easydrop.toml
type Config struct {
	App    AppConfig    `toml:"app"`
	Server ServerConfig `toml:"server"`
	Build  BuildConfig  `toml:"build"`
	Driver DriverConfig `toml:"driver"`
	Nginx  NginxConfig  `toml:"nginx"`
}

type AppConfig struct {
	Name            string `toml:"name"`
	Port            int    `toml:"port"`
	HealthCheckPath string `toml:"health_check_path,omitempty"` // Default: "/"
}

type ServerConfig struct {
	Host     string `toml:"host"`
	User     string `toml:"user"`
	SSHKey   string `toml:"ssh_key,omitempty"` // Default: "~/.ssh/id_rsa"
	Password string `toml:"password,omitempty"`
	Port     int    `toml:"port,omitempty"` // Default: 22
}

type BuildConfig struct {
	Strategy string `toml:"strategy"` // "remote" or "local", default: "remote"
	Registry string `toml:"registry,omitempty"`
	Image    string `toml:"image,omitempty"`
	NoCache  bool   `toml:"no_cache,omitempty"`
}

type DriverConfig struct {
	Type        string `toml:"type"`                   // "solo", "compose", "swarm", default: "solo"
	ComposeFile string `toml:"compose_file,omitempty"` // Default: "docker-compose.yml"
}

type NginxConfig struct {
	Domain string `toml:"domain"`
	SSL    bool   `toml:"ssl"`
	Email  string `toml:"email,omitempty"`
}

// Application acts as the compiled runtime context passing through the Core
type Application struct {
	Config *Config
}

type AppStatus struct {
	Status    string `json:"status"` // "Up", "Down", "Restarting"
	Uptime    string `json:"uptime"`
	SSLStatus string `json:"ssl_status"`
}
