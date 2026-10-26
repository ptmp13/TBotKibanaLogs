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
		rulesPath = "rules.yaml"
	}
	rules, err := loadRules(rulesPath)
	if err != nil {
		log.Printf("Warning: Could not load rules from %s: %s", rulesPath, err)
	} else {
		log.Printf("Loaded %d custom rules", len(rules))
	}

	ticker := time.NewTicker(30 * time.Second)
	log.Println("Starting alert poller...")

	for {
		select {
		case <-ticker.C:
			processAlerts(es, esIndex, bot, rules)
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

func processAlerts(es *elasticsearch.Client, index string, bot *tgbotapi.BotAPI, rules map[string]Rule) {
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

	for _, hit := range r.Hits.Hits {
		doc := hit.Source
		log.Printf("Processing alert: %s", hit.ID)

		// 2. Send Telegram alert
		if bot != nil {
			// Get Chat ID from env or somewhere. If not set, we can't send.
			// In this example, I'll assume we want to send it.
			// Ideally, the chat_id might be in the document or config.
			chatID := new(int64)
			// For now, let's just log it if chat ID is missing
			fmt.Sscanf(os.Getenv("TG_CHAT_ID"), "%d", chatID)

			if *chatID != 0 {
				var msgText string
				if rule, ok := rules[doc.RuleName]; ok {
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
					statusEmoji := "🚨"
					if doc.Status == "recovered" {
						statusEmoji = "✅"
					}
					msgText = fmt.Sprintf("%s *Alert: %s*\n\nStatus: %s\n%s\n\nRule: %s\nTime: %s",
						statusEmoji, doc.AlertID, doc.Status, doc.ContextMessage, doc.RuleName, doc.Timestamp.Format(time.RFC3339))
				}

				msg := tgbotapi.NewMessage(*chatID, msgText)
				msg.ParseMode = "Markdown"
				_, err := bot.Send(msg)
				if err != nil {
					log.Printf("Error sending Telegram message: %s", err)
					continue // Don't update ES if notification failed? Or maybe we should?
				}
			} else {
				log.Println("TG_CHAT_ID not set, skipping Telegram notification.")
			}
		}

		// 3. Update tsend to "true"
		updateDoc := map[string]any{
			"doc": map[string]any{
				"tsend": "true",
			},
		}
		var upBuf bytes.Buffer
		if err := json.NewEncoder(&upBuf).Encode(updateDoc); err != nil {
			log.Printf("Error encoding update doc for %s: %s", hit.ID, err)
			continue
		}

		upRes, err := es.Update(
			index,
			hit.ID,
			&upBuf,
			es.Update.WithContext(ctx),
			es.Update.WithRefresh("true"),
		)
		if err != nil {
			log.Printf("Error executing update request for document %s: %s", hit.ID, err)
			continue
		}
		defer upRes.Body.Close()

		if upRes.IsError() {
			var errResp map[string]any
			json.NewDecoder(upRes.Body).Decode(&errResp)
			log.Printf("Error updating document %s. Status: %s, Response: %+v", hit.ID, upRes.Status(), errResp)
		} else {
			log.Printf("Successfully processed and updated alert %s", hit.ID)
		}
	}
}
