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
	Name string `toml:"name"`
	// Port is the port the app listens on INSIDE the container.
	//
	// omitempty so that `easydrop init` can write a config WITHOUT a port when
	// it could not detect one: a missing key reads as "not set yet" and fails
	// validation with an actionable message, whereas `port = 0` would look like
	// a value somebody chose. A guessed default is worse than neither – see
	// OD-04.
	Port int `toml:"port,omitempty"`
	// HostPort is the port published on the host. Optional: when omitted it
	// defaults to Port, so existing configs keep their behavior. The
	// Blue-Green managed pair is {HostPort, HostPort+1}.
	HostPort        int    `toml:"host_port,omitempty"`
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
	Type        string `toml:"type"`                   // "single", "compose", "swarm", default: "single"
	ComposeFile string `toml:"compose_file,omitempty"` // Default: "docker-compose.yml"
	// BlueGreen enables zero-downtime Blue-Green swaps. Default false:
	// plain deploy stops the old container and starts the new one in place
	// (brief downtime). Overridable per-run via `deploy --blue-green`.
	BlueGreen bool `toml:"blue_green,omitempty"`
}

type NginxConfig struct {
	Domain string `toml:"domain"`
	SSL    bool   `toml:"ssl"`
	Email  string `toml:"email,omitempty"`
}

// Application acts as the compiled runtime context passing through the Core.
// Image is the resolved image reference drivers must run: LocalBuilder sets it
// to the pushed registry reference (build.strategy = "local"), otherwise it
// stays empty and drivers use the remote-built easydrop/<name>:latest.
type Application struct {
	Config *Config
	Image  string
}

type AppStatus struct {
	Status    string `json:"status"` // "Up", "Down", "Restarting"
	Uptime    string `json:"uptime"`
	SSLStatus string `json:"ssl_status"`
}
