# Session Notes - Elasticsearch Telegram Alerter

This session focused on developing a Go application to bridge Elasticsearch alerts with Telegram notifications.

## Steps Taken

1.  **Project Initialization**:
    *   Created `go.mod` and initialized the project `TBotKibanaLogs`.
    *   Added dependencies: `github.com/elastic/go-elasticsearch/v8` and `github.com/go-telegram-bot-api/telegram-bot-api/v5`.

2.  **Implementation of Core Logic (`main.go`)**:
    *   **Elasticsearch Integration**: Configured a v8 client to connect to an Elasticsearch cluster. Now supports HTTPS with skipped SSL verification and API Key authentication.
    *   **Polling Mechanism**: Implemented a background loop that searches for documents in a specified index where the field `"tsend"` is exactly `"false"`.
    *   **Telegram Notification**: Integrated the Telegram Bot API to send the `context_message` from the alert document to a specified chat.
    *   **Document Update**: After successful notification, the application updates the document in Elasticsearch, setting `"tsend": "true"` to prevent duplicate alerts. Switched to `es.Update` method and added better error logging to troubleshoot issues with updates not persisting.
    *   **Configuration**: Set up the application to read configuration (URLs, tokens, etc.) from environment variables. Added support for `ES_API_KEY` and `RULES_FILE`.
    *   **Alert Customization**: Implemented a YAML-based rule system to customize Telegram alert titles and messages based on the `rule_name`. Added `gopkg.in/yaml.v3` dependency.

3.  **Build & Verification**:
    *   Ran `go mod tidy` to manage dependencies.
    *   Successfully compiled the application using `go build -o tbot_kibana_logs main.go`.

4.  **Documentation**:
    *   Created `README.md` for usage instructions.
    *   Created `SESSION_NOTES.md` for project history.

### Step 6: Alert Filtering & Recovery Support
- Initial version added a `bool` query to exclude `status: "recovered"`.
- Revised in Step 7 to include `status: "recovered"` for recovery alerts.
- Updated `AlertDoc` struct to include `status`, `URL`, `SpaceID`, `Link`, and `Alert` fields.

### Step 7: Recovery Alert Support & Template Formatting
- Updated `Rule` struct to support `recoveryTitle` and `recoveryMessage`.
- Modified `processAlerts` search query to only filter by `tsend: "false"`, allowing both firing and recovery alerts.
- Implemented `text/template` support for all alert titles and messages.
- Updated `rules.yaml` and `README.md` to document the new template syntax and recovery support.
- Verified build.
