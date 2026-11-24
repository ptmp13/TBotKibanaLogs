package main

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"html"
	"log"
	"net/http"
	"os"
	"strings"
	"text/template"
	"time"

	"github.com/elastic/go-elasticsearch/v8"
	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"gopkg.in/yaml.v3"
)

type AlertDoc struct {
	RuleID         string    `json:"rule_id"`
	RuleName       string    `json:"rule_name"`
	AlertID        string    `json:"alert_id"`
	ContextMessage string    `json:"context_message"`
	Timestamp      time.Time `json:"@timestamp"`
	TSend          string    `json:"tsend"`
	Status         string    `json:"status"`
	URL            string    `json:"URL"`
	SpaceID        string    `json:"SpaceID"`
	Link           string    `json:"link"`
	Alert          string    `json:"alert"`
}

type SearchResponse struct {
	Hits struct {
		Hits []struct {
			ID     string   `json:"_id"`
			Source AlertDoc `json:"_source"`
		} `json:"hits"`
	} `json:"hits"`
}

type Rule struct {
	RuleName        string `yaml:"rule_name"`
	Title           string `yaml:"title"`
	Message         string `yaml:"message"`
	RecoveryTitle   string `yaml:"recoveryTitle"`
	RecoveryMessage string `yaml:"recoveryMessage"`
}

func loadRules(path string) (map[string]Rule, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return make(map[string]Rule), nil
		}
		return nil, err
	}

	var rules []Rule
	if err := yaml.Unmarshal(data, &rules); err != nil {
		return nil, err
	}

	ruleMap := make(map[string]Rule)
	for _, r := range rules {
		ruleMap[r.RuleName] = r
	}
	return ruleMap, nil
}

func main() {
	// Configuration (In a real app, these should come from env vars or config file)
	esURL := os.Getenv("ES_URL")
	if esURL == "" {
		esURL = "http://localhost:9200"
	}
	kibanaURL := os.Getenv("KIBANAURL")
	if kibanaURL == "" {
		kibanaURL = "http://localhost"
	}
	esIndex := os.Getenv("ES_INDEX")
	if esIndex == "" {
		esIndex = "telegaalert"
	}
	esAPIKey := os.Getenv("ES_API_KEY")
	tgToken := os.Getenv("TG_TOKEN")
	// For demonstration, we'll try to get them, but handle missing values gracefully

	if tgToken == "" {
		log.Println("Warning: TG_TOKEN not set. Telegram alerts will be skipped.")
	}

	// Initialize Elasticsearch client
	cfg := elasticsearch.Config{
		Addresses: []string{esURL},
		APIKey:    esAPIKey,
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
		},
	}
	es, err := elasticsearch.NewClient(cfg)
	if err != nil {
		log.Fatalf("Error creating the ES client: %s", err)
	}

	// Initialize Telegram client
	var bot *tgbotapi.BotAPI
	if tgToken != "" {
		bot, err = tgbotapi.NewBotAPI(tgToken)
		if err != nil {
			log.Printf("Error creating Telegram bot: %s", err)
		}
	}

	// Load custom rules
	rulesPath := os.Getenv("RULES_FILE")
	if rulesPath == "" {
		rulesPath = "configs/rules.yaml"
	}
	rules, err := loadRules(rulesPath)
	if err != nil {
		log.Printf("Warning: Could not load rules from %s: %s", rulesPath, err)
	} else {
		log.Printf("Loaded %d custom rules", len(rules))
	}

	ticker := time.NewTicker(30 * time.Second)
	log.Println("Starting alert poller...")

	// Start health check server
	go func() {
		http.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
			w.Write([]byte("ok"))
		})
		port := os.Getenv("PORT")
		if port == "" {
			port = "8080"
		}
		log.Printf("Health check server starting on port %s", port)
		if err := http.ListenAndServe(":"+port, nil); err != nil {
			log.Printf("Health check server failed: %s", err)
		}
	}()

	for {
		select {
		case <-ticker.C:
			processAlerts(es, esIndex, bot, rules, kibanaURL)
		}
	}
}

func formatTemplate(tmplStr string, data any) string {
	tmpl, err := template.New("msg").Parse(tmplStr)
	if err != nil {
		return tmplStr // Fallback to raw string
	}
	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, data); err != nil {
		return tmplStr
	}
	return buf.String()
}

func processAlerts(es *elasticsearch.Client, index string, bot *tgbotapi.BotAPI, rules map[string]Rule, kibanaURL string) {
	ctx := context.Background()

	// 1. Search for documents where tsend is "false"
	query := map[string]any{
		"query": map[string]any{
			"term": map[string]any{
				"tsend": "false",
			},
		},
	}
	var buf bytes.Buffer
	if err := json.NewEncoder(&buf).Encode(query); err != nil {
		log.Printf("Error encoding query: %s", err)
		return
	}

	res, err := es.Search(
		es.Search.WithContext(ctx),
		es.Search.WithIndex(index),
		es.Search.WithBody(&buf),
		es.Search.WithTrackTotalHits(true),
	)
	if err != nil {
		log.Printf("Error searching documents: %s", err)
		return
	}
	defer res.Body.Close()

	if res.IsError() {
		log.Printf("Error search response: %s", res.String())
		return
	}

	var r SearchResponse
	if err := json.NewDecoder(res.Body).Decode(&r); err != nil {
		log.Printf("Error parsing search response: %s", err)
		return
	}

	if len(r.Hits.Hits) == 0 {
		return
	}

	// 2. Group alerts by rule_name + SpaceID + status
	type groupKey struct {
		RuleName string
		SpaceID  string
		Status   string
	}
	groups := make(map[groupKey][]string) // key -> list of document IDs
	groupDocs := make(map[groupKey]AlertDoc)

	for _, hit := range r.Hits.Hits {
		key := groupKey{
			RuleName: hit.Source.RuleName,
			SpaceID:  hit.Source.SpaceID,
			Status:   hit.Source.Status,
		}
		groups[key] = append(groups[key], hit.ID)
		if _, ok := groupDocs[key]; !ok {
			groupDocs[key] = hit.Source
		}
	}

	for key, ids := range groups {
		doc := groupDocs[key]
		log.Printf("Processing group: %+v with %d alerts", key, len(ids))

		// Escape doc fields for HTML
		doc.SpaceID = html.EscapeString(doc.SpaceID)
		doc.RuleName = html.EscapeString(doc.RuleName)
		doc.ContextMessage = html.EscapeString(doc.ContextMessage)
		// doc.Link should probably NOT be escaped fully if it's used in href,
		// but Telegram HTML requires escaping & in href too.
		doc.Link = html.EscapeString(doc.Link)

		// 3. Send Telegram alert (one per group)
		if bot != nil {
			chatID := new(int64)
			fmt.Sscanf(os.Getenv("TG_CHAT_ID"), "%d", chatID)

			if *chatID != 0 {
				var msgText string
				rule, ok := rules[doc.RuleName]
				if !ok {
					rule, ok = rules["default"]
				}

				// Prepare Kibana Link
				fullKibanaURL := strings.TrimSuffix(kibanaURL, "/") + "/" + strings.TrimPrefix(doc.Link, "/")

				if ok {
					title := rule.Title
					message := rule.Message
					if doc.Status == "recovered" {
						if rule.RecoveryTitle != "" {
							title = rule.RecoveryTitle
						}
						if rule.RecoveryMessage != "" {
							message = rule.RecoveryMessage
						}
					}
					// Replace ${KIBANAURL} in templates with the prepared URL
					// We do this before template execution so it can be used if needed,
					// but wait, the template might already have it.
					// Actually, os.ExpandEnv handles ${KIBANAURL}.
					// But we want to ensure it's joined correctly.
					// Let's set KIBANAURL env var for this goroutine/process context if we want ExpandEnv to work well,
					// or just replace it manually.

					formattedTitle := formatTemplate(title, doc)
					formattedMessage := formatTemplate(message, doc)
					msgText = formattedTitle + "\n\n" + formattedMessage
				} else {
					if doc.Status == "recovered" {
						msgText = fmt.Sprintf("✅ Recovered: %s - %s", doc.SpaceID, doc.RuleName)
					} else {
						msgText = fmt.Sprintf("🚨 %s - %s\n%s\nURL: <a href=\"%s\">link</a>",
							doc.SpaceID, doc.RuleName, doc.ContextMessage, fullKibanaURL)
					}
				}

				os.Setenv("KIBANAURL", strings.TrimSuffix(kibanaURL, "/"))
				msgText = os.ExpandEnv(msgText)

				msg := tgbotapi.NewMessage(*chatID, msgText)
				msg.ParseMode = "HTML"
				_, err := bot.Send(msg)
				if err != nil {
					log.Printf("Error sending Telegram message for group %+v: %s. Message was: %q", key, err, msgText)
					continue
				}
			} else {
				log.Println("TG_CHAT_ID not set, skipping Telegram notification.")
			}
		}

		// 4. Update all documents in the group to tsend: "true"
		// Using _update_by_query for efficiency
		updateQuery := map[string]any{
			"script": map[string]any{
				"source": "ctx._source.tsend = 'true'",
				"lang":   "painless",
			},
			// Select by document IDs found in the search above instead of `<field>.keyword`
			// sub-fields: data streams usually have explicit mappings where these fields are
			// already `keyword` and have no `.keyword` sub-field, so such a query matches nothing.
			"query": map[string]any{
				"bool": map[string]any{
					"filter": []any{
						map[string]any{"ids": map[string]any{"values": ids}},
						map[string]any{"term": map[string]any{"tsend": "false"}},
					},
				},
			},
		}

		var upBuf bytes.Buffer
		if err := json.NewEncoder(&upBuf).Encode(updateQuery); err != nil {
			log.Printf("Error encoding update query for group %+v: %s", key, err)
			continue
		}

		upRes, err := es.UpdateByQuery(
			[]string{index},
			es.UpdateByQuery.WithContext(ctx),
			es.UpdateByQuery.WithBody(&upBuf),
			es.UpdateByQuery.WithRefresh(true),
			es.UpdateByQuery.WithConflicts("proceed"),
		)
		if err != nil {
			log.Printf("Error executing update by query for group %+v: %s", key, err)
			continue
		}

		if upRes.IsError() {
			log.Printf("Error response from update by query for group %+v: %s", key, upRes.String())
			upRes.Body.Close()
			continue
		}

		var upResult struct {
			Updated          int64 `json:"updated"`
			VersionConflicts int64 `json:"version_conflicts"`
			Failures         []any `json:"failures"`
		}
		decErr := json.NewDecoder(upRes.Body).Decode(&upResult)
		upRes.Body.Close()
		if decErr != nil {
			log.Printf("Error parsing update by query response for group %+v: %s", key, decErr)
			continue
		}
		if upResult.Updated == 0 || len(upResult.Failures) > 0 {
			log.Printf("Update by query for group %+v updated %d of %d docs (conflicts: %d, failures: %v)",
				key, upResult.Updated, len(ids), upResult.VersionConflicts, upResult.Failures)
		} else {
			log.Printf("Successfully updated %d alerts in group %+v", upResult.Updated, key)
		}
	}
}
