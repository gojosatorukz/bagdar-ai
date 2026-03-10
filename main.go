package main

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"fmt"
	"html/template"
	"io/ioutil"
	"log"
	"net/http"
	"os" // 🔥 ДОБАВЛЕН ИМПОРТ ДЛЯ РАБОТЫ С ПОРТАМИ ОБЛАКА
	"regexp"
	"time"

	"github.com/google/uuid"
	_ "github.com/lib/pq"
	"golang.org/x/crypto/bcrypt"
)

const (
	dbConnStr  = "postgres://brashiduly:HMFSyICt2e@a1-postgres1.alem.ai:30100/aicoach?sslmode=disable"
	alemAPIKey = "sk-DVt8_6iLKL1F6rqMhEwVdg"
)

var db *sql.DB
var sessions = make(map[string]int)

type LLMRequest struct {
	Model    string    `json:"model"`
	Messages []Message `json:"messages"`
}
type Message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}
type LLMResponse struct {
	Choices []struct {
		Message Message `json:"message"`
	} `json:"choices"`
}

type User struct {
	ID         int
	Email      string
	Name       string
	Weight     float64
	Height     int
	Goal       string
	Experience string
	AIPlan     template.HTML
	IsPremium  bool
	PlanType   string // "Monthly" или "Yearly"
}

type WorkoutLog struct {
	Date       string
	Exercise   string
	WeightUsed float64
	Reps       int
}

type DashboardData struct {
	User  User
	Logs  []WorkoutLog
	Error string
}

func main() {
	var err error
	db, err = sql.Open("postgres", dbConnStr)
	if err != nil {
		log.Fatal(err)
	}
	defer db.Close()

	initDB()

	http.HandleFunc("/", handleHome)
	http.HandleFunc("/register", handleRegister)
	http.HandleFunc("/login", handleLogin)
	http.HandleFunc("/logout", handleLogout)
	http.HandleFunc("/dashboard", handleDashboard)
	http.HandleFunc("/generate", handleGenerate)
	http.HandleFunc("/log_workout", handleLogWorkout)

	// БИЗНЕС-РОУТЫ
	http.HandleFunc("/billing", handleBilling)     // Страница выбора тарифа
	http.HandleFunc("/checkout", handleCheckout)   // Страница ввода карты
	http.HandleFunc("/subscribe", handleSubscribe) // Финал оплаты

	// РАЗДАЧА СТАТИКИ (ЛОГОТИП)
	http.Handle("/static/", http.StripPrefix("/static/", http.FileServer(http.Dir("static"))))

	// 🔥 УМНАЯ НАСТРОЙКА ПОРТА ДЛЯ RENDER.COM
	port := os.Getenv("PORT")
	if port == "" {
		port = "8080" // Фолбек для локального запуска на твоем ноуте
	}

	fmt.Println("🚀 Bagdar.AI успешно запущен на порту: " + port)
	log.Fatal(http.ListenAndServe(":"+port, nil))
}

func initDB() {
	db.Exec(`CREATE TABLE IF NOT EXISTS users (
        id SERIAL PRIMARY KEY,
        email VARCHAR(255) UNIQUE NOT NULL,
        password_hash VARCHAR(255) NOT NULL,
        name VARCHAR(100),
        weight NUMERIC,
        height INTEGER,
        goal VARCHAR(100),
        experience VARCHAR(100),
        ai_plan TEXT,
        is_premium BOOLEAN DEFAULT FALSE,
        plan_type VARCHAR(50),
        created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
    )`)
	db.Exec(`ALTER TABLE users ADD COLUMN IF NOT EXISTS is_premium BOOLEAN DEFAULT FALSE;`)
	db.Exec(`ALTER TABLE users ADD COLUMN IF NOT EXISTS plan_type VARCHAR(50);`)
	db.Exec(`CREATE TABLE IF NOT EXISTS workout_logs (
        id SERIAL PRIMARY KEY,
        user_id INTEGER REFERENCES users(id),
        date DATE DEFAULT CURRENT_DATE,
        exercise_name VARCHAR(255),
        weight_used NUMERIC,
        reps INTEGER,
        created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
    )`)
}

func getUserID(r *http.Request) (int, bool) {
	cookie, err := r.Cookie("session_token")
	if err != nil {
		return 0, false
	}
	userID, exists := sessions[cookie.Value]
	return userID, exists
}

// --- ХЕНДЛЕРЫ ---

func handleBilling(w http.ResponseWriter, r *http.Request) {
	userID, loggedIn := getUserID(r)
	if !loggedIn {
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return
	}

	var user User
	db.QueryRow("SELECT name, is_premium, plan_type FROM users WHERE id=$1", userID).Scan(&user.Name, &user.IsPremium, &user.PlanType)

	tmpl := template.Must(template.ParseFiles("templates/billing.html"))
	tmpl.Execute(w, user)
}

func handleCheckout(w http.ResponseWriter, r *http.Request) {
	_, loggedIn := getUserID(r)
	if !loggedIn {
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return
	}

	plan := r.URL.Query().Get("plan") // "monthly" или "yearly"
	tmpl := template.Must(template.ParseFiles("templates/checkout.html"))
	tmpl.Execute(w, plan)
}

func handleSubscribe(w http.ResponseWriter, r *http.Request) {
	userID, loggedIn := getUserID(r)
	if !loggedIn || r.Method != http.MethodPost {
		return
	}

	plan := r.FormValue("plan")
	db.Exec("UPDATE users SET is_premium = TRUE, plan_type = $1 WHERE id = $2", plan, userID)
	http.Redirect(w, r, "/dashboard?success=premium", http.StatusSeeOther)
}

func handleHome(w http.ResponseWriter, r *http.Request) {
	if _, loggedIn := getUserID(r); loggedIn {
		http.Redirect(w, r, "/dashboard", http.StatusSeeOther)
		return
	}
	tmpl := template.Must(template.ParseFiles("templates/auth.html"))
	tmpl.Execute(w, nil)
}

func handleRegister(w http.ResponseWriter, r *http.Request) {
	r.ParseForm()
	name, email, password := r.FormValue("name"), r.FormValue("email"), r.FormValue("password")
	hashedPassword, _ := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	db.Exec("INSERT INTO users (name, email, password_hash) VALUES ($1, $2, $3)", name, email, string(hashedPassword))
	http.Redirect(w, r, "/?success=registered", http.StatusSeeOther)
}

func handleLogin(w http.ResponseWriter, r *http.Request) {
	r.ParseForm()
	email, password := r.FormValue("email"), r.FormValue("password")
	var id int
	var hash string
	err := db.QueryRow("SELECT id, password_hash FROM users WHERE email=$1", email).Scan(&id, &hash)
	if err != nil || bcrypt.CompareHashAndPassword([]byte(hash), []byte(password)) != nil {
		http.Redirect(w, r, "/?error=invalid_credentials", http.StatusSeeOther)
		return
	}
	sessionToken := uuid.NewString()
	sessions[sessionToken] = id
	http.SetCookie(w, &http.Cookie{Name: "session_token", Value: sessionToken, Expires: time.Now().Add(24 * time.Hour), HttpOnly: true})
	http.Redirect(w, r, "/dashboard", http.StatusSeeOther)
}

func handleLogout(w http.ResponseWriter, r *http.Request) {
	cookie, _ := r.Cookie("session_token")
	if cookie != nil {
		delete(sessions, cookie.Value)
	}
	http.SetCookie(w, &http.Cookie{Name: "session_token", Value: "", Expires: time.Now().Add(-time.Hour)})
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

func handleDashboard(w http.ResponseWriter, r *http.Request) {
	userID, loggedIn := getUserID(r)
	if !loggedIn {
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return
	}

	var data DashboardData
	var rawPlan sql.NullString
	db.QueryRow("SELECT id, name, email, COALESCE(weight, 0), COALESCE(height, 0), COALESCE(goal, ''), COALESCE(experience, ''), ai_plan, is_premium FROM users WHERE id=$1", userID).
		Scan(&data.User.ID, &data.User.Name, &data.User.Email, &data.User.Weight, &data.User.Height, &data.User.Goal, &data.User.Experience, &rawPlan, &data.User.IsPremium)

	if rawPlan.Valid {
		data.User.AIPlan = template.HTML(rawPlan.String)
	}

	rows, _ := db.Query("SELECT TO_CHAR(date, 'DD.MM.YYYY'), exercise_name, weight_used, reps FROM workout_logs WHERE user_id=$1 ORDER BY date DESC, id DESC", userID)
	defer rows.Close()
	for rows.Next() {
		var l WorkoutLog
		rows.Scan(&l.Date, &l.Exercise, &l.WeightUsed, &l.Reps)
		data.Logs = append(data.Logs, l)
	}
	tmpl := template.Must(template.ParseFiles("templates/dashboard.html"))
	tmpl.Execute(w, data)
}

func handleGenerate(w http.ResponseWriter, r *http.Request) {
	userID, loggedIn := getUserID(r)
	if !loggedIn || r.Method != http.MethodPost {
		return
	}
	r.ParseForm()
	weight, height, goal, experience, days := r.FormValue("weight"), r.FormValue("height"), r.FormValue("goal"), r.FormValue("experience"), r.FormValue("days")
	db.Exec("UPDATE users SET weight=$1, height=$2, goal=$3, experience=$4 WHERE id=$5", weight, height, goal, experience, userID)

	systemPrompt := "Ты профессиональный тренер. Составь план тренировок и БЖУ.\nВЫДАЙ ТОЛЬКО ВНУТРЕННИЙ HTML КОНТЕНТ.\nКАТЕГОРИЧЕСКИ ЗАПРЕЩЕНО ИСПОЛЬЗОВАТЬ: <html>, <body>, <head>, <style>, <table>, <div>, markdown."
	userPrompt := fmt.Sprintf("Цель: %s, Опыт: %s, Вес: %s кг, Рост: %s см. Тренировок в неделю: %s.", goal, experience, weight, height, days)

	reqBody := LLMRequest{Model: "qwen3", Messages: []Message{{Role: "system", Content: systemPrompt}, {Role: "user", Content: userPrompt}}}
	jsonData, _ := json.Marshal(reqBody)
	req, _ := http.NewRequest("POST", "https://llm.alem.ai/v1/chat/completions", bytes.NewBuffer(jsonData))
	req.Header.Set("Authorization", "Bearer "+alemAPIKey)
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{Timeout: 60 * time.Second}
	resp, err := client.Do(req)
	if err == nil && resp.StatusCode == 200 {
		body, _ := ioutil.ReadAll(resp.Body)
		var llmResp LLMResponse
		json.Unmarshal(body, &llmResp)
		if len(llmResp.Choices) > 0 {
			aiPlan := llmResp.Choices[0].Message.Content
			reTags := regexp.MustCompile(`(?i)</?(html|body|head|!DOCTYPE|meta|title|div|table|style|script)[^>]*>`)
			aiPlan = reTags.ReplaceAllString(aiPlan, "")
			db.Exec("UPDATE users SET ai_plan=$1 WHERE id=$2", aiPlan, userID)
		}
	}
	http.Redirect(w, r, "/dashboard", http.StatusSeeOther)
}

func handleLogWorkout(w http.ResponseWriter, r *http.Request) {
	userID, loggedIn := getUserID(r)
	if !loggedIn || r.Method != http.MethodPost {
		return
	}
	r.ParseForm()
	exercise, weight, reps := r.FormValue("exercise"), r.FormValue("weight"), r.FormValue("reps")
	db.Exec("INSERT INTO workout_logs (user_id, exercise_name, weight_used, reps) VALUES ($1, $2, $3, $4)", userID, exercise, weight, reps)
	http.Redirect(w, r, "/dashboard", http.StatusSeeOther)
}
