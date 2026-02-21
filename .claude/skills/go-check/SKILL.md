---
name: go-check
description: Build and vet the voltgpt bot, reporting compilation or static analysis errors
disable-model-invocation: true
---

Run the following commands in /home/bot/dev/bots/voltgpt and report results:

1. `/usr/local/go/bin/go vet ./...` — report all warnings
2. `/usr/local/go/bin/go build -o voltgpt` — report any build errors
3. If both pass, confirm the binary was updated successfully
4. If either fails, show the full error output so it can be fixed
