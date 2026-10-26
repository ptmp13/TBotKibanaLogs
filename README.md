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

## How it works

1. The application polls Elasticsearch every 30 seconds.
2. it searches for documents where `"tsend": "false"`.
3. For each match, it sends the `context_message` to Telegram.
4. Once sent, it updates the document in Elasticsearch, setting `"tsend": "true"`.

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
   export TG_TOKEN="your_bot_token"
   export TG_CHAT_ID="-100123456789"
   ./tbot_kibana_logs
   ```
