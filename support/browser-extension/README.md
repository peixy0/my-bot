# My Bot Browser Bridge

Requires Chrome 116 or newer. Load this directory as an unpacked extension from `chrome://extensions` with Developer mode enabled.

Open the extension's Details page, select Extension options, configure the bot WebSocket URL, then select **Connect**. The bridge is disconnected by default and does not reconnect after a Chrome restart until you connect it again.

```text
ws://127.0.0.1:8020/browser
```

Enable the bot endpoint in `config.yaml`:

```yaml
browser:
  enabled: true
  listen_addr: "127.0.0.1:8020"
  path: "/browser"
  bearer_token: ""
  request_timeout_seconds: 30
```

For remote endpoints, terminate TLS in a reverse proxy and use `wss://` with a strong bearer token. The settings page rejects remote `ws://` URLs; plain `ws://` is only accepted for localhost.

The extension requests broad page access because an agent can create a tab for any site and then inspect or control that tab. It only exposes tabs created by the agent's browser scope; it cannot list or operate ordinary Chrome tabs. The `debugger` permission is used by all page interaction tools (snapshot, click, type, select_option, press_key, evaluate) via the Chrome DevTools Protocol to produce `isTrusted=true` events that React, Vue, and native form behaviors recognize. Do not install the extension in a profile whose signed-in sessions you do not want the bot to access.

## Connection states

- **Disconnected**: no bridge connection exists. Select Connect to start one.
- **Connecting**: the extension is opening the configured WebSocket.
- **Connected**: authentication completed and the broker may send browser actions.
- **Reconnecting**: a previously connected bridge was interrupted; retries use bounded exponential backoff.
- **Connection error**: check the endpoint, TLS certificate, bearer token, and broker logs.

Saving new settings disconnects the active bridge. Select Connect again to apply the new endpoint or token.

## Verification

1. Reload the unpacked extension and confirm the options page shows Disconnected.
2. Save the local endpoint, select Connect, and confirm Connected after the broker is running.
3. Stop the broker and confirm the status changes to Reconnecting; select Disconnect to stop retries.
4. Start a browser task, then reload the extension service worker from `chrome://extensions`; confirm the task's owned tabs remain listed.
5. Try snapshot or input on a Chrome internal page and confirm the tool returns a clear access error.
6. Open DevTools on an agent-owned page, call `browser_evaluate`, and confirm the debugger attachment failure is reported without leaving a stuck connection.
7. Use `browser_snapshot`, then `browser_click` on an element in a React or native form; confirm the click triggers the form's submit handler (CDP `isTrusted=true` event).
