---
okf_version: "0.2"
---

# AI Agent Memory — knowledge bundle

Long-term, team-wide memory for AI agents, served over MCP. This bundle is the
machine-readable mirror of the public pages at {{BASE_URL}}: every concept
document below states, in plain Markdown, what the corresponding HTML page says,
so an agent or answer engine can read the product without parsing a page.

The bundle follows the Open Knowledge Format (OKF) v0.2. Every non-reserved
`.md` file carries YAML frontmatter with a non-empty `type`.

## Product

* [What AI Agent Memory is](./landing.md) - the product, the memory-palace data model, pricing and the full FAQ; mirrors the landing page.

## Guides

* [Sandboxed installs](./sandboxes.md) - one isolated agent config per project, and what `init` / `load` commit to a repository.
* [Agent self-install (CLI)](./claude-guide.md) - the guide an agent fetches to install the kit on macOS or Linux by itself.
* [Setup without the CLI (Windows, VS Code, Cursor, Claude Desktop)](./windows-guide.md) - writing the MCP config by hand where the installer cannot run.

## Discovery

* [Sitemap index]({{BASE_URL}}/sitemap.xml) - points at the page sitemap and this bundle's sitemap.
* [AI sitemap]({{BASE_URL}}/ai-sitemap.xml) - every document in this bundle.
* [llms.txt]({{BASE_URL}}/llms.txt) - the same map in llmstxt.org form.
* [llms-full.txt]({{BASE_URL}}/llms-full.txt) - this entire bundle concatenated into one fetch.
