// Command sehataqua-monitor memeriksa stok hadiah di loyalty.sehataqua.co.id
// dan mengirim notifikasi Telegram saat stok tersedia.
//
// Stok dibaca lewat endpoint katalog TADA yang bersifat publik, jadi tidak
// perlu login / OTP WhatsApp sama sekali.
package main


import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
)

type config struct {
	baseURL   string
	programID string
	itemID    int
	stateFile string
	remindFor time.Duration
	tgToken   string
	tgChatID  string
	dryRun    bool
}

type itemsResponse struct {
	Data []item `json:"data"`
}

type item struct {
	ItemID     int       `json:"itemId"`
	ItemName   string    `json:"itemName"`
	ItemStatus string    `json:"itemStatus"`
	Variants   []variant `json:"variants"`
}

type variant struct {
	VariantID     int     `json:"variantId"`
	VariantName   string  `json:"variantName"`
	Price         float64 `json:"price"`
	StatusVariant string  `json:"statusVariant"`
	HasStock      bool    `json:"hasStock"`
	IsLimited     bool    `json:"isLimited"`
	Stock         int     `json:"stock"`
}

type state struct {
	Available      bool      `json:"available"`
	Stock          int       `json:"stock"`
	LastNotifiedAt time.Time `json:"last_notified_at"`
	LastCheckedAt  time.Time `json:"last_checked_at"`
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	cfg := loadConfig()

	// Jangan simpan state saat dry run supaya percobaan lokal tidak
	// mengubah status yang dipakai cron.
	prev, err := loadState(cfg.stateFile)
	if err != nil {
		return err
	}

	it, err := fetchItemWithRetry(cfg)
	if err != nil {
		return err
	}

	available, stock, hasUnlimited := evaluate(it)
	price := firstPrice(it)
	variantName := firstVariantName(it)

	fmt.Printf("item=%d name=%q available=%v stock=%d unlimited=%v price=%.0f\n",
		it.ItemID, it.ItemName, available, stock, hasUnlimited, price)

	now := time.Now().UTC()
	shouldNotify := false
	reason := ""
	switch {
	case available && !prev.Available:
		shouldNotify, reason = true, "stok baru tersedia"
	case available && prev.Available && now.Sub(prev.LastNotifiedAt) >= cfg.remindFor:
		shouldNotify, reason = true, "pengingat berkala (masih tersedia)"
	case !available && prev.Available:
		fmt.Println("stok kembali habis; tidak mengirim notifikasi")
	}

	if shouldNotify {
		text := notificationText(cfg, it.ItemName, variantName, stock, hasUnlimited, price)
		if err := sendTelegram(cfg, text); err != nil {
			return fmt.Errorf("kirim Telegram (%s): %w", reason, err)
		}
		prev.LastNotifiedAt = now
		fmt.Printf("notifikasi terkirim (%s)\n", reason)
	} else if !available {
		fmt.Println("stok masih habis; tidak ada notifikasi")
	} else {
		fmt.Println("stok masih tersedia; belum waktunya pengingat")
	}

	prev.Available = available
	prev.Stock = stock
	prev.LastCheckedAt = now

	if cfg.dryRun {
		fmt.Println("dry run: state tidak disimpan")
		return nil
	}
	return saveState(cfg.stateFile, prev)
}

// evaluate mengembalikan status ketersediaan dari semua varian aktif.
// hasUnlimited menandakan ada varian ber-stok tanpa batas (hasStock=true,
// stock=0), misalnya item digital.
func evaluate(it *item) (available bool, stock int, hasUnlimited bool) {
	for _, v := range it.Variants {
		if v.StatusVariant != "active" {
			continue
		}
		if v.eHasStock || v.Stock > 0 {
			available = true
			stock += v.Stock
			if v.HasStock && v.Stock == 0 {
				hasUnlimited = true
			}
		}
	}
	return available, stock, hasUnlimited
}

func firstPrice(it *item) float64 {
	for _, v := range it.Variants {
		if v.StatusVariant == "active" {
			return v.Price
		}
	}
	if len(it.Variants) > 0 {
		return it.Variants[0].Price
	}
	return 0
}

func firstVariantName(it *item) string {
	for _, v := range it.Variants {
		if v.StatusVariant == "active" && v.VariantName != "" {
			return v.VariantName
		}
	}
	return ""
}

func notificationText(cfg config, itemName, variantName string, stock int, unlimited bool, price float64) string {
	var b strings.Builder
	b.WriteString("🎉 Stok hadiah tersedia!\n\n")
	b.WriteString(itemName)
	if variantName != "" && variantName != itemName {
		b.WriteString(" — ")
		b.WriteString(variantName)
	}
	b.WriteString("\n")
	if price > 0 {
		b.WriteString("Harga: ")
		b.WriteString(formatNumber(price))
		b.WriteString(" poin\n")
	}
	if unlimited {
		b.WriteString("Stok: tersedia (tanpa batas)\n")
	} else {
		b.WriteString("Stok: ")
		b.WriteString(strconv.Itoa(stock))
		b.WriteString("\n")
	}
	b.WriteString("\nBuka: ")
	b.WriteString(rewardURL(cfg))
	b.WriteString("\n\nDicek: ")
	b.WriteString(time.Now().Format("02 Jan 2006 15:04 MST"))
	return b.String()
}

func rewardURL(cfg config) string {
	return cfg.baseURL + "/rewards/" + strconv.Itoa(cfg.itemID)
}

func fetchItemWithRetry(cfg config) (*item, error) {
	var lastErr error
	for attempt := 1; attempt <= 3; attempt++ {
		it, err := fetchItem(cfg)
		if err == nil {
			return it, nil
		}
		lastErr = err
		fmt.Printf("percobaan %d gagal: %v\n", attempt, err)
		if attempt < 3 {
			time.Sleep(3 * time.Second)
		}
	}
	return nil, lastErr
}

func fetchItem(cfg config) (*item, error) {
	u, err := url.Parse(cfg.baseURL + "/api/tada/v2/m-integration/items")
	if err != nil {
		return nil, err
	}
	q := u.Query()
	q.Set("programId", cfg.programID)
	q.Set("page", "1")
	q.Set("perPage", "200")
	q.Set("catalogType", "rewardCatalog")
	u.RawQuery = q.Encode()

	req, err := http.NewRequest(http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "sehataqua-monitor/1.0")

	client := &http.Client{Timeout: 25 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP %d: %s", resp.StatusCode, truncate(string(body), 200))
	}

	var parsed itemsResponse
	if err := json.Unmarshal(body, &parsed); err != nil {
		return nil, fmt.Errorf("parse JSON: %w", err)
	}
	for i := range parsed.Data {
		if parsed.Data[i].ItemID == cfg.itemID {
			return &parsed.Data[i], nil
		}
	}
	return nil, fmt.Errorf("item %d tidak ditemukan di katalog", cfg.itemID)
}

func sendTelegram(cfg config, text string) error {
	if cfg.dryRun {
		fmt.Println("--- dry run, isi notifikasi ---")
		fmt.Println(text)
		fmt.Println("-------------------------------")
		return nil
	}
	if cfg.tgToken == "" || cfg.tgChatID == "" {
		return errors.New("TELEGRAM_BOT_TOKEN dan TELEGRAM_CHAT_ID wajib diisi")
	}

	payload, err := json.Marshal(map[string]any{
		"chat_id":                  cfg.tgChatID,
		"text":                     text,
		"disable_web_page_preview": false,
	})
	if err != nil {
		return err
	}

	endpoint := "https://api.telegram.org/bot" + cfg.tgToken + "/sendMessage"
	req, err := http.NewRequest(http.MethodPost, endpoint, bytes.NewReader(payload))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{Timeout: 25 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("HTTP %d: %s", resp.StatusCode, truncate(string(body), 300))
	}
	return nil
}

func loadConfig() config {
	cfg := config{
		baseURL:   env("BASE_URL", "https://loyalty.sehataqua.co.id"),
		programID: env("PROGRAM_ID", "1000112251223"),
		stateFile: env("STATE_FILE", "state.json"),
		tgToken:   strings.TrimSpace(os.Getenv("TELEGRAM_BOT_TOKEN")),
		tgChatID:  strings.TrimSpace(os.Getenv("TELEGRAM_CHAT_ID")),
		dryRun:    envBool("DRY_RUN"),
	}
	cfg.baseURL = strings.TrimRight(cfg.baseURL, "/")
	//cfg.itemID = envInt("TARGET_ITEM_ID", 38)
	cfg.itemID = envInt("TARGET_ITEM_ID", 38)

	remindMinutes := envInt("REMIND_MINUTES", 5)
	if remindMinutes < 1 {
		remindMinutes = 1
	}
	cfg.remindFor = time.Duration(remindMinutes) * time.Minute
	return cfg
}

func loadState(path string) (state, error) {
	var s state
	body, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return s, nil
	}
	if err != nil {
		return s, err
	}
	if err := json.Unmarshal(body, &s); err != nil {
		// State rusak jangan menghentikan pemantauan; anggap belum ada.
		fmt.Printf("peringatan: state tidak valid, diabaikan: %v\n", err)
		return state{}, nil
	}
	return s, nil
}

func saveState(path string, s state) error {
	body, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, body, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func env(key, def string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return def
}

func envInt(key string, def int) int {
	v := strings.TrimSpace(os.Getenv(key))
	if v == "" {
		return def
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		fmt.Printf("peringatan: %s=%q bukan angka, pakai %d\n", key, v, def)
		return def
	}
	return n
}

func envBool(key string) bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv(key))) {
	case "1", "true", "yes", "on":
		return true
	default:
		return false
	}
}

// formatNumber memberi pemisah ribuan gaya Indonesia (79.000).
func formatNumber(f float64) string {
	n := int64(f)
	s := strconv.FormatInt(n, 10)
	neg := strings.HasPrefix(s, "-")
	if neg {
		s = s[1:]
	}
	var parts []string
	for len(s) > 3 {
		parts = append([]string{s[len(s)-3:]}, parts...)
		s = s[:len(s)-3]
	}
	parts = append([]string{s}, parts...)
	out := strings.Join(parts, ".")
	if neg {
		out = "-" + out
	}
	return out
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
