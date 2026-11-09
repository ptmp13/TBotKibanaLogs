# Elasticsearch Telegram Alerter

A Go application that monitors an Elasticsearch index for new alerts and sends them to a Telegram chat.

## Prerequisites

- Go 1.26+
- Access to an Elasticsearch 9.3+ cluster.
- A Telegram Bot token (from @BotFather).
- The Chat ID where alerts should be sent.

## Environment Variables

To run the application, you must set the following environment variables:

| Variable | Description | Default |
| :--- | :--- | :--- |
| `ES_URL` | The URL of your Elasticsearch instance. | `http://localhost:9200` |
| `ES_INDEX` | The index to monitor for alerts. | `telegaalert` |
| `ES_API_KEY` | The API Key for Elasticsearch authentication. | (Optional if not using API Key) |
| `TG_TOKEN` | Your Telegram Bot API token. | (Required for Telegram alerts) |
| `TG_CHAT_ID` | The Telegram Chat ID where messages will be sent. | (Required for Telegram alerts) |
| `RULES_FILE` | Path to the YAML file containing custom alert rules. | `rules.yaml` |
| `KIBANAURL` | The base URL for Kibana (including protocol). | `http://localhost` |

## Custom Alert Rules

You can customize the alert messages based on the `rule_name` field by creating a YAML file (default `rules.yaml`). A special `rule_name: "default"` can be used to define a fallback template for all rules not explicitly defined.

Example `rules.yaml`:

```yaml
- rule_name: "default"
  title: "🚨 {{.SpaceID}} - {{.RuleName}}"
  recoveryTitle: "✅ Recovered: {{.SpaceID}} - {{.RuleName}}"
  message: |
    {{.ContextMessage}}
    URL: <a href="${KIBANAURL}{{.Link}}">link</a>
```

The application uses Telegram's **HTML parse mode**, allowing you to use tags like `<a>`, `<b>`, `<i>`, and `<code>` in your templates. Environment variables like `${KIBANAURL}` are expanded in the final message.

### Alert Filtering Logic
- The application polls Elasticsearch every 30 seconds.
- It searches for documents where `"tsend": "false"`.
- It supports both firing and recovery alerts.
- For each match, it sends the alert to Telegram using the appropriate rule template.
- Once sent, it updates the document to set `"tsend": "true"`.

### Rule Templates
Rules support `text/template` syntax for `title`, `message`, `recoveryTitle`, and `recoveryMessage`. Available fields from the Elasticsearch document include:
- `{{.RuleID}}` - `rule_id`
- `{{.RuleName}}` - `rule_name`
- `{{.AlertID}}` - `alert_id`
- `{{.ContextMessage}}` - `context_message`
- `{{.Timestamp}}` - `@timestamp`
- `{{.Status}}` - `status` (e.g., "recovered" or "firing")
- `{{.URL}}` - `URL`
- `{{.SpaceID}}` - `SpaceID`
- `{{.Link}}` - `link` (path part of the Kibana URL)
- `{{.Alert}}` - `alert`

Environment variables in the form `${VAR_NAME}` can also be used in the message templates and will be expanded at runtime.

Example `rules.yaml` with specific rule and recovery support:

```yaml
- rule_name: "Elasticsearch query rule"
  title: "🚨 {{.SpaceID}} - {{.RuleName}}"
  recoveryTitle: "✅ Recovered: {{.SpaceID}} - {{.RuleName}}"
  recoveryMessage: "The alert has cleared."
  message: |
    <b>Status:</b> Firing
    {{.ContextMessage}}
    <a href="${KIBANAURL}{{.Link}}">View in Kibana</a>
```

Note: The application is configured to skip SSL verification for HTTPS connections to Elasticsearch.

## Running the Application

1. **Build the application**:
   ```bash
   go build -o tbot_kibana_logs main.go
   ```

2. **Set environment variables and run**:
   ```bash
   export ES_URL="https://your-elastic-host:9200"
   export ES_INDEX="telegaalert"
   export ES_API_KEY="your_api_key_here"
   export KIBANAURL="http://192.168.97.11:5601"
   export TG_TOKEN="your_bot_token"
   export TG_CHAT_ID="-100123456789"
   ./tbot_kibana_logs
   ```
