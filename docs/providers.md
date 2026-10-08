# Providers

A **provider** is a service that runs models for elencode. A **model** belongs to exactly
one provider, and is written `provider/model` wherever that matters:
`anthropic/claude-opus-5`, `openai/gpt-5.5`, `chatgpt/gpt-6.1-sol`, `moonshot/kimi-k3`.

| Provider | What you need |
|---|---|
| `anthropic` | an API key from [console.anthropic.com](https://console.anthropic.com/settings/keys) |
| `openai` | an API key from [platform.openai.com](https://platform.openai.com/api-keys) |
| `chatgpt` | a ChatGPT plan; you sign in, and use its allowance instead of API credits |
| `moonshot` | an API key from [platform.kimi.ai](https://platform.kimi.ai/console/api-keys), for Moonshot AI's Kimi models |

## Getting started

Start elencode in the folder you want to work on:

```sh
cd ~/src/my-project
elencode
```

The first time, no provider is connected yet, so elencode asks you to choose one:

```
No provider is connected yet. Choose one to start with:

› anthropic  not connected · API key
│ chatgpt    not connected · sign in with a ChatGPT plan
│ openai     not connected · API key
│ moonshot   not connected · API key
```

Choose with ↑/↓ (or type to filter) and enter. Esc quits.

**With an API key** (`anthropic`, `openai`, `moonshot`), paste the key when asked. It is not shown
as you type:

```
Paste your Anthropic API key
(create one at https://console.anthropic.com/settings/keys)

> ••••••••••••••••••••

enter save · esc cancel
```

elencode tries the key before saving it. A key the provider rejects is not saved, and
you are asked again.

**With ChatGPT**, elencode opens the sign-in page in your browser. Sign in there and come
back. `c` copies the link if the browser did not open.

Once the provider is connected, elencode says where the key went and shows the
provider's models:

```
Connected anthropic; the key is saved in ~/.config/elencode/credentials.json.

› claude-opus-5     anthropic · Claude Opus 5
│ claude-sonnet-5   anthropic · Claude Sonnet 5
│ claude-fable-5    anthropic · Claude Fable 5
│ …
```

Pick one with enter, or press esc to stay on the provider's default. Then start
typing your request. elencode remembers the model for next time.

### Connecting from the shell

The same works without starting a session:

```sh
$ elencode connect anthropic
Paste your Anthropic API key (create one at https://console.anthropic.com/settings/keys):
Connected anthropic; the key is saved in ~/.config/elencode/credentials.json.
```

Nothing is shown as you type or paste the key. With no provider named,
`elencode connect` lists them and asks you to name one.

On a machine without a browser, sign in to ChatGPT with a code you enter on another
device:

```sh
elencode connect chatgpt --device
```

To set a key from a password manager or a script, pipe it in. It never appears in your
shell history:

```sh
pass show anthropic | elencode connect anthropic
```

## Managing providers

| In a session | From the shell | Does |
|---|---|---|
| `/connect` | `elencode connect` | lists the providers, and connects the one you choose |
| `/connect <id>` | `elencode connect <id>` | connects a provider, or replaces its credential |
| `/disconnect <id>` | `elencode disconnect <id>` | forgets a provider's credential |

`/login` and `/logout` (`elencode login`, `elencode logout`) do the same as `/connect`
and `/disconnect`.

`/connect` on its own shows which providers are connected and where each credential
comes from:

```
› anthropic  connected · API key from $ANTHROPIC_API_KEY
│ chatgpt    not connected · sign in with a ChatGPT plan
│ openai     connected · API key in credentials.json
│ moonshot   not connected · API key
```

Choosing a provider that is already connected replaces its key or sign-in. Esc closes
the list. `/config` lists the same providers alongside your other settings, and
`elencode connect` piped somewhere other than a terminal prints them:

```
$ elencode connect | cat
anthropic  connected · API key from $ANTHROPIC_API_KEY
chatgpt    not connected · sign in with a ChatGPT plan
openai     connected · API key in credentials.json
moonshot   not connected · API key
```

Every connected provider is available at once. `/model` offers the models of all of
them, and switching models is what switches providers.

`disconnect` only forgets what elencode stored. It does not revoke the key or the
ChatGPT sign-in with the provider: do that on the provider's website if the key may have
leaked.

## Choosing a model

`/model` opens a picker of every model your providers serve. `/model <id>` switches
directly, and `elencode model [id]` does the same from the shell. Your choice is saved as
`model` in `config.json`, and the next session starts on it.

The list of models ships with elencode, along with which of them can reason. A model
released after your version is still reachable by naming its provider:
`/model openai/gpt-7`. elencode assumes such a model does not reason.

If the model you last used belongs to a provider that is no longer connected, elencode
starts on another connected provider's default and tells you so.

`thinking_enabled` and `thinking_effort` in `config.json` control reasoning for models
that support it.

Moonshot's Kimi models always reason, whatever `thinking_enabled` says. Only `kimi-k3`
takes a `thinking_effort`, and only `low`, `high` and `max`: `medium` is sent as
`high`, and `xhigh` as `max`.

## Where things are stored

In `~/.config/elencode/` (or `$XDG_CONFIG_HOME/elencode/`):

| File | Holds |
|---|---|
| `config.json` | your settings: the model, thinking. No secrets: safe to share or keep in your dotfiles |
| `credentials.json` | API keys, and your ChatGPT sign-in |

`credentials.json` must be readable by you alone. elencode creates it that way, and
warns at startup if that changed:

```
elencode: credentials.json can be read by others (mode 0644): run chmod 600 ~/.config/elencode/credentials.json
```

`/config` (or `elencode config`) shows your settings, each provider, and where
`config.json` and `credentials.json` are.

## Keys in environment variables

Instead of storing a key, you can set it in the environment. It wins over a stored key:

```sh
export ANTHROPIC_API_KEY=sk-ant-…
export OPENAI_API_KEY=sk-…
export MOONSHOT_API_KEY=sk-…
```

`/connect` shows which one is in use. A key from the environment cannot be removed with
`disconnect`: unset the variable instead. When you connect a provider whose key the
environment already supplies, elencode tells you before asking.

These are the only variables elencode reads to reach a provider. Others that the
providers' own tools honour, such as `ANTHROPIC_BASE_URL`, `ANTHROPIC_AUTH_TOKEN`,
`OPENAI_BASE_URL`, `OPENAI_ORG_ID` or `OPENAI_PROJECT_ID`, have no effect. As for any
program, `HTTPS_PROXY`, `NO_PROXY` and `SSL_CERT_FILE` apply to the network connection.

## Troubleshooting

- **`anthropic: the API key was rejected (401 Unauthorized)`.** The key is mistyped,
  revoked, or belongs to the other provider. Create a new one at the provider's
  website.
- **`The key could not be checked (…), so it was saved unchecked`.** elencode could not
  reach the provider, so it saved the key without trying it. If your first message
  fails with an authentication error, run `connect` again.
- **You saved a key, but elencode keeps using another one.** An environment variable
  wins over a stored key. `/connect` shows which is in use.
- **ChatGPT says you are signed out.** The sign-in expired or was revoked. Run
  `elencode connect chatgpt` again.
