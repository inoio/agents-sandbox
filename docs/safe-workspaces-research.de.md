# Sichere Workspaces für Agentic Coding

## Zusammenfassung

Ich suche einen Ersatz für mein Bubblewrap-Setup. Geschaut habe ich dabei aus inoio-Perspektive, um auch Mehrwert für euch zu schaffen. Features:

* (must) sicher, starke Isolation (Fokus auf Unfälle über Bösartigkeit)
* (must) gute Developer Experience (DX)
* (strong should) plattformübergreifend (macOS + Linux)
* (strong should) Open Source

**Kein Tool erfüllt alle Anforderungen.** Für gute DX lohnt sich unterschiedliches Tooling, das zu unterschiedlichen Isolations-Abwägungen führt:

* **CLI-Agenten** wie [opencode](https://github.com/anomalyco/opencode), [Claude Code](https://code.claude.com/docs/en/overview) oder Codex profitieren von einer echten MicroVM: stärkere Isolation (Hypervisor-Grenze), sicheres Docker-in-Docker. 

* **IDE-Agenten** wie Junie oder Cursor funktionieren gut mit Devcontainers, z.B. [JetBrains Dev Containers](https://www.jetbrains.com/help/idea/open-project-with-dev-container-natively.html), [VS Code Dev Containers](https://containers.dev/). Das schiebt den Projekt-Workspace in einen Container. Die Isolation ist hier weit weniger stark als bei VM-basierten Ansätzen.

---

## Problemkontext

AI-Coding-Agenten führen Code mit den Entwickler-Rechten aus: Shells, Packages, Repos, Tests, Push. Zwei Fehlermodi: 

* **versehentliche Schäden** (z.B. `rm -rf` außerhalb des Projekts)
* **Prompt Injection** (adversarialer Text in gelesenen Ressourcen (z.B. Issue-Texts), der z.B. Secrets exfiltriert) 

Wichtigste Maßnahmen:

* **Egress-Kontrolle** (ausgehenden Netzwerk-Verkehr außer einer Positivliste blockieren)
* **Dateisystem-Grenzen** (Möglichst nur Projektdateien schreibbar, System-/Home-Dateien nicht schreibbar/lesbar, Credentials nicht lesbar).
* **Prozess-Grenzen** (z.B. Docker Daemon nicht zwischen System und Sandbox teilen)
* **Kernel-Grenze** (Sandbox-VM)

---

## Zwei Workflows, zwei Ansätze

Der architektonische Fork wird durch **wo der Agent läuft** getrieben.

### Workflow A - IDE-basierte Agenten (Devcontainer)

Devcontainer ist praktisch erforderlich: [JetBrains native Dev Containers](https://www.jetbrains.com/help/idea/open-project-with-dev-container-natively.html) (IntelliJ 2025.3+) und [VS Code Dev Containers](https://containers.dev/) erwarten beide eine Docker-Runtime. Das IDE-Backend muss im Container leben, damit das Agent-Plugin das Projekt-Dateisystem sieht. Junie erfordert explizit eine `devcontainer.json` (laut [JetBrains/junie-workflows](https://github.com/jetbrains-junie/junie-workflows)). Isolation auf VM-Ebene verlangt, dass die IDE in der VM läuft (nicht verfolgt).

**Ansatz:** Docker-/Podman-Runtime + `devcontainer.json` + Proxy-Sidecar für Egress-Kontrolle + Secret-Injection am Proxy. 

**Abwägungen:** Schwächere Isolation (Container-Escape = Host-Kompromittierung, allerdings fügt [OrbStack](https://orbstack.dev/) auf macOS eine Hypervisor-Grenze hinzu); Docker-in-Docker im Container ist problematisch (braucht Privileged Mode oder Read-only-Socket-Proxy); erfordert eine Linux-VM-Runtime auf macOS.

### Workflow B - CLI-Agenten (MicroVM)

Vorteile:

* **Hypervisor-Grenze** - der Agent kompromittiert nur die VM
* **Sicheres Docker-in-Docker** - die VM hat ihren eigenen Docker-Daemon
* **Keine Docker-Desktop-/Colima-Abhängigkeit** auf macOS - die MicroVM *ist* die Linux-Umgebung.

---

## Workflow A - Optionsvergleich (IDE-Agenten)

| Dimension | [agent-sandbox](https://github.com/mattolson/agent-sandbox) | [sandcat](https://github.com/VirtusLab/sandcat) |
|---|---|---|
| **Lizenz** | MIT | Apache-2.0 |
| **Aktivität** | 191★, 598 Commits, v0.16.1 | 170★, 154 Commits |
| **macOS** | ✅ Colima/Docker/OrbStack/Podman | ✅ Docker/Colima |
| **Linux** | ✅ jede Docker-Runtime | ✅ jede Docker-Runtime |
| **Explizit unterstützte Agenten** | 8: Claude, Codex, Gemini, OpenCode, Pi, Factory, Copilot, Hermes | Claude, Cursor (kein OpenCode) |
| **IDE** | ✅ VS Code + JetBrains Devcontainer | ✅ VS Code + JetBrains Devcontainer |
| **Egress** | [mitmproxy](https://mitmproxy.org/)-Sidecar + iptables; Default-Deny; Request-aware (Host/Method/Path/Query) | [mitmproxy](https://mitmproxy.org/) via [WireGuard](https://www.wireguard.com/) (erfasst gesamten TCP/UDP, nicht nur HTTP_PROXY-bewusst); DNS-Filterung |
| **Secret-Injection** | Host-Secret-Verzeichnis → Proxy injiziert zur Request-Zeit | Platzhalter-Substitution; [1Password](https://1password.com/)-Integration via `op://` |



**Empfehlung**: [agent-sandbox](https://github.com/mattolson/agent-sandbox) MITM-Proxy + iptables + Secret-Injection ist ein starkes Defense-in-Depth-Modell; acht Agenten dokumentiert; 
[sandcat](https://github.com/VirtusLab/sandcat)s WireGuard-Proxy ist technisch überlegen (erfasst gesamten Traffic), aber die Agenten-Abdeckung ist geringer.

---

## Workflow B - Optionsvergleich (CLI-Agenten)

| Dimension | [matchlock](https://github.com/jingkaihe/matchlock) | [microsandbox](https://github.com/superradcompany/microsandbox) |
|---|---|---|
| **Lizenz** | MIT | Apache-2.0 |
| **Aktivität** | 606★, 510 Commits, v0.2.16 | 7k★, 659 Commits, v0.6.6 |
| **macOS** | ✅ Apple Silicon ([Virtualization.framework](https://developer.apple.com/documentation/virtualization)) | ✅ Apple Silicon ([libkrun](https://github.com/containers/libkrun)) |
| **Linux** | ✅ KVM ([Firecracker](https://firecracker-microvm.github.io/)) | ✅ KVM |
| **Windows** | ❌ | ✅ WHP |
| **Boot** | <1 Sekunde | <100 ms |
| **Egress** | [MITM-Proxy](https://mitmproxy.org/) + TLS-Interception; `--allow-host`; nftables DNAT (Linux), [gVisor](https://gvisor.dev/) Userspace-TCP (macOS) | Network-Allowlisting; Secret-Injection |
| **SDKs** | Go, Python, TypeScript | Rust, Python, TypeScript, Go |
| **MCP** | Beispiele (ACP, Playwright) | ✅ [MCP-Server](https://github.com/superradcompany/microsandbox-mcp) + [Agent Skills](https://github.com/superradcompany/skills) |
| **Reife** | Experimental, 38 Releases | Beta, 49 Releases, [YC](https://www.ycombinator.com/)-Unterstützt |

---

## Anhang - vollständig betrachtete Landschaft

**Container-basiert (Workflow A):** 
* [agent-sandbox](https://github.com/mattolson/agent-sandbox) (MIT, 191★) - gewählt; 8 Agenten, mitmproxy+iptables, JetBrains+VS Code. 
* [sandcat](https://github.com/VirtusLab/sandcat) (Apache-2.0, 170★) - WireGuard-mitmproxy, 1Password; nur Claude+Cursor. 
* [leash](https://github.com/strongdm/leash) (Apache-2.0, 581★) nutzt [Cedar](https://docs.cedarpolicy.com/)-Policy-Engine, vollständiges FS+Network-Telemetry, Control-UI; läuft als Root (schwächer); opencode im Default-Image. 
* [tsk-tsk](https://github.com/dtormoen/tsk-tsk) (MIT, 168★) - Async-Task-Delegation, parallele Agenten, Branch-basiertes Review; nur Claude+Codex.

**MicroVM-basiert (Workflow B):** 
* [matchlock](https://github.com/jingkaihe/matchlock) (MIT, 606★) nutzt [Firecracker](https://firecracker-microvm.github.io/)/[VZ](https://developer.apple.com/documentation/virtualization), MITM-Proxy, VFS-Hooks, `docker-in-sandbox`
* [microsandbox](https://github.com/superradcompany/microsandbox) (Apache-2.0, 7k★) nutzt [libkrun](https://github.com/containers/libkrun), Sub-100ms-Boot, MCP+Skills

**Ausgeschlossen:** 
* [Docker Sandboxes](https://docs.docker.com/ai/sandboxes/) `sbx` ([docker/sbx-releases](https://github.com/docker/sbx-releases), proprietär) - unter Linux bisher nur Ubuntu supportet
* [Kaiden](https://openkaiden.ai/) - zu plattformartig (Governance, Secret-Vault, Model-Routing)
* [BitBot](https://github.com/ManuelKugelmann/BitBot) (3★)
* [devc](https://github.com/grahambrooks/ai-dev-container) (1★)
* [fkeil/agentbox](https://github.com/fkeil/agentbox) (4★) / [tsilva/agentbox](https://github.com/tsilva/agentbox) (2★)

**Referenzen:** 
* [VirtusLab-Blog](https://virtuslab.com/blog/ai/sandboxing-llm-coding-agents-part1) - Landschafts-Erhebung. 
* [Anthropic](https://www.anthropic.com/engineering/how-we-contain-claude) - Egress + Dateisystem-Grenzen halten. 
* [env.dev-Guide](https://env.dev/guides/securing-dev-environment-ai-agents) - Devcontainer-Hardening. 
* [Dev Containers Spec](https://containers.dev/). 
* [JetBrains Gateway](https://www.jetbrains.com/remote-development/gateway/) - potenzieller MicroVM-Integrationspfad.
