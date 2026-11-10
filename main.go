package main

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
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
					formattedTitle := formatTemplate(title, doc)
					formattedMessage := formatTemplate(message, doc)
					msgText = formattedTitle + "\n\n" + formattedMessage
				} else {
					if doc.Status == "recovered" {
						msgText = fmt.Sprintf("✅ Recovered: %s - %s", doc.SpaceID, doc.RuleName)
					} else {
						msgText = fmt.Sprintf("🚨 %s - %s\n%s\nURL: <a href=\"%s%s\">link</a>",
							doc.SpaceID, doc.RuleName, doc.ContextMessage, kibanaURL, doc.Link)
					}
				}
				msgText = os.ExpandEnv(msgText)

				msg := tgbotapi.NewMessage(*chatID, msgText)
				msg.ParseMode = "HTML"
				_, err := bot.Send(msg)
				if err != nil {
					log.Printf("Error sending Telegram message for group %+v: %s", key, err)
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
			"query": map[string]any{
				"bool": map[string]any{
					"filter": []any{
						map[string]any{"term": map[string]any{"rule_name.keyword": key.RuleName}},
						map[string]any{"term": map[string]any{"SpaceID.keyword": key.SpaceID}},
						map[string]any{"term": map[string]any{"status.keyword": key.Status}},
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
		)
		if err != nil {
			log.Printf("Error executing update by query for group %+v: %s", key, err)
			continue
		}
		defer upRes.Body.Close()

		if upRes.IsError() {
			log.Printf("Error response from update by query for group %+v: %s", key, upRes.String())
		} else {
			log.Printf("Successfully updated all alerts in group %+v", key)
		}
	}
}
