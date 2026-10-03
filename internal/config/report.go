package config

import (
	"fmt"
	"strings"
)

// Report renders what `init` detected and what the caller must do next.
//
// There is exactly one Report, shared by the CLI (printed to stdout) and the
// MCP server (returned as text content). The two interfaces used to word this
// separately and drifted – the CLI listed where to look for a port, MCP did not –
// and an agent reading a weaker variant than the human is exactly the failure
// mode worth avoiding here: it would either invent a port or bounce the
// question back at the user instead of reading the code, which usually states
// it.
func (r *ScaffoldResult) Report() string {
	if r == nil || r.Config == nil {
		return "init produced no configuration"
	}
	var b strings.Builder

	fmt.Fprintf(&b, "wrote easydrop.toml\n")
	fmt.Fprintf(&b, "  app     %s (%s)\n", r.App.Name, r.AppNameSource)
	if r.PortMissing() {
		fmt.Fprintf(&b, "  port    NOT SET – nothing in the project states it\n")
	} else {
		fmt.Fprintf(&b, "  port    %d (%s)\n", r.App.Port, r.PortSource)
	}
	if r.Stack != "" {
		fmt.Fprintf(&b, "  stack   %s\n", r.Stack)
	}
	fmt.Fprintf(&b, "  driver  %s\n", r.Driver.Type)

	if r.PortMissing() {
		b.WriteString(missingPortAdvice)
	}
	if r.Driver.Type == "compose" && !r.PortMissing() {
		fmt.Fprintf(&b, "\nnote: with the compose driver app.port must equal the port %s\n"+
			"      publishes on the host – that is what the Nginx vhost proxies to.\n",
			r.Driver.ComposeFile)
	}
	fmt.Fprintf(&b, "\nnext: set [server] – host and user. init always writes host = \"localhost\".\n")
	return b.String()
}

// missingPortAdvice is the block shown when detection found nothing. It is
// written for whoever reads it – human or agent – and, where the two want
// different things, says both: an agent is told how to resolve the port itself
// (it usually can, and asking the user for something the repository states is
// a poor experience), while the human gets the same method plus the two
// supported ways to supply the port up front.
//
// The "why" is load-bearing and stays: without it, a port looks like a routine
// detail, and the failure it causes is not visible until much later.
const missingPortAdvice = `
ACTION REQUIRED: set [app].port in easydrop.toml before deploying.

  easydrop found no port: no EXPOSE in the Dockerfile, no compose ports
  mapping, no --port flag, no PORT= in .env, no app.listen()/ListenAndServe().
  Nothing was defaulted on purpose, because a wrong port builds fine and only
  breaks the deploy: a healthcheck timeout for the single driver, and a 502
  from the proxy for compose/swarm, which have no HTTP probe at all.

  To resolve it:
    1. read the code and find where the server binds – app.listen(N),
       ListenAndServe(":N"), uvicorn.run(port=N), --port in a Procfile or a
       Dockerfile CMD, PORT in a settings module. A port the project states
       itself beats any framework convention.
    2. write app.port into easydrop.toml – easydrop has no tool for that, so
       edit the file yourself.
    3. then deploy.

  Or supply the port up front and skip all of this:
      easydrop init --port 8080          (terminal)
      init_project { "port": 8080 }      (MCP)

  Ask the user only if the code leaves the port genuinely ambiguous.
`
