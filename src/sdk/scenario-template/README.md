# Scenario template

The starting point for a new scenario. The easy path is the scaffolder:

```bash
labctl scenario new <name>      # creates scenarios/<name>/ from this template
labctl scenario verify <name>   # green out of the box
```

Copy this directory manually if you prefer. The `# yaml-language-server` modeline
gives inline validation in editors (see `.vscode/settings.json`). Authoring guide:
`docs/authoring/first-scenario.md`.
