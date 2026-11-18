---
title: "A first flag in ten minutes"
description: "Compose up, bootstrap, a project, a flag and an SDK key, evaluated from the Go or JavaScript SDK, then flipped and seen to change."
---

# A first flag in ten minutes

This page takes you from nothing to a flag that a Go or a JavaScript program reads, and then changes the flag while the program runs. You need Docker with Compose, `curl`, and Go 1.26 or newer or Node.js 22 or newer. Everything runs on your machine; nothing here is meant for production (see [Installing Flagnetic](install.md) for that).

## 1. Start Flagnetic

```bash
git clone https://github.com/azverinus/flagnetic.git
cd flagnetic/deploy
echo "FLAGNETIC_SECRET_KEY=$(openssl rand -base64 32)" > .env   # once; keep this file
docker compose up -d
```

This starts PostgreSQL, applies the migrations and starts a [replica](glossary.md#structure) on http://localhost:8080. The server is ready when this answers `200`:

```bash
curl -s -o /dev/null -w '%{http_code}\n' http://localhost:8080/readyz
```

## 2. Bootstrap the first admin

An instance has no member until you [bootstrap](install.md#with-docker-compose) one:

```bash
docker compose exec flagnetic flagnetic admin bootstrap --email you@example.com
```

The command prints a generated password and an API token (`fn_pat_…`), each once. Open http://localhost:8080 and sign in with the email and the password to see the console. The rest of this page uses the token, so the commands can be pasted; each step names the console screen that does the same.

```bash
export FLAGNETIC_TOKEN=fn_pat_…   # the token the command printed
```

## 3. Create a project and a flag

A [project](glossary.md#structure) holds flags and their environments. A new one comes with `development`, `staging` and `production`.

```bash
curl -s -X POST http://localhost:8080/api/v1/projects \
  -H "Authorization: Bearer $FLAGNETIC_TOKEN" -H 'Content-Type: application/json' \
  -d '{"key": "quickstart", "name": "Quickstart"}'
```

Then a boolean flag in it. A [flag](flags.md) is defined once for the project and starts switched off in every environment:

```bash
curl -s -X POST http://localhost:8080/api/v1/projects/quickstart/flags \
  -H "Authorization: Bearer $FLAGNETIC_TOKEN" -H 'Content-Type: application/json' \
  -d '{"key": "new_checkout", "type": "boolean", "name": "New checkout"}'
```

In the console this is **New flag** on the project's **Flags** page.

## 4. Create an SDK key

A program reads flags with an [SDK key](sdks/keys.md) of one environment. A backend takes a `server` key, which downloads the ruleset and evaluates flags inside the program:

```bash
curl -s -X POST http://localhost:8080/api/v1/projects/quickstart/environments/development/sdk-keys \
  -H "Authorization: Bearer $FLAGNETIC_TOKEN" -H 'Content-Type: application/json' \
  -d '{"kind": "server", "name": "quickstart"}'
```

The answer holds the key's `value` (`fn_srv_…`) once, and never again. In the console it is **New SDK key** on the **SDK keys** tab of the environment. Keep the value in your shell:

```bash
export FLAGNETIC_SDK_KEY=fn_srv_…   # the value the command answered
```

## 5. Evaluate the flag from an SDK

Pick your language. Each program asks for the flag once a second and prints what it serves.

### Go

```bash
mkdir quickstart-go && cd quickstart-go
go mod init example.com/quickstart
go get github.com/azverinus/flagnetic/sdks/go
```

Save this as `main.go`:

```go
package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"time"

	flagnetic "github.com/azverinus/flagnetic/sdks/go"
)

func main() {
	c, err := flagnetic.New(os.Getenv("FLAGNETIC_SDK_KEY"), "http://localhost:8080")
	if err != nil {
		log.Fatal(err)
	}
	defer c.Close(context.Background())

	readyCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	fmt.Println("stage:", c.Ready(readyCtx))
	cancel()

	fc := flagnetic.Context{Units: map[string]string{"user": "u-1001"}}
	for {
		fmt.Println("new_checkout:", c.Bool(context.Background(), fc, "new_checkout", false))
		time.Sleep(time.Second)
	}
}
```

```bash
go run .
```

### JavaScript

```bash
mkdir quickstart-js && cd quickstart-js
npm init -y && npm pkg set type=module
npm install flagnetic
```

Save this as `index.mjs`:

```js
import { createServer } from "flagnetic/server";

const flagnetic = createServer(process.env.FLAGNETIC_SDK_KEY, "http://localhost:8080");
console.log("stage:", await flagnetic.ready(2000));

const context = { units: { user: "u-1001" } };
setInterval(() => {
  console.log("new_checkout:", flagnetic.getBool(context, "new_checkout", false));
}, 1000);
```

```bash
node index.mjs
```

Either program prints `stage: fresh`, meaning it holds the environment's current ruleset, and then `new_checkout: false`, the flag's off value. The last argument of every getter is the built-in default, served only when the flag cannot be evaluated at all, for example before the first ruleset arrives or when its key is misspelled.

## 6. Flip the flag

Leave the program running, and in another terminal (with `FLAGNETIC_TOKEN` exported there too) switch the flag on in `development`. A new flag's configuration is version 1, and a write names the version it is based on in `If-Match`, so that two people never overwrite each other unseen:

```bash
curl -s -X PUT http://localhost:8080/api/v1/projects/quickstart/flags/new_checkout/environments/development \
  -H "Authorization: Bearer $FLAGNETIC_TOKEN" -H 'Content-Type: application/json' -H 'If-Match: 1' \
  -d '{"on": true, "off_variation": "false", "fallthrough": "true"}'
```

Within a couple of seconds the program prints `new_checkout: true`, with no restart and no polling of yours: the SDK holds a [ruleset stream](sdks/stream.md) open and fetches the new ruleset when a replica announces it. In the console, the same change is made on the flag's `development` tab. Send the same request with `"on": false` and `If-Match: 2` to switch it off again.

## Where to go next

- [Flags](flags.md): rules, rollouts, overrides, prerequisites, the kill switch.
- [Targeting](targeting.md) and [segments](segments.md): who gets which value, by the attributes of a context.
- [Experiments](experiments.md): assigning units to variations and recording exposures.
- [Choosing an SDK](sdks/README.md): every SDK, and [SDK keys](sdks/keys.md) for the browser and the apps, which take a `client` key and learn served values only.
- A project to look around in: import the [demo project](install.md#try-it-with-the-demo-project).
- [Installing Flagnetic](install.md) and [Operations](operations.md) for a real deployment.

When you are done, `docker compose down -v` in `deploy` stops Flagnetic and deletes its data.
