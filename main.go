package main

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/gorilla/mux"
	_ "github.com/lib/pq"
	"database/sql"
)

func getEnv(key, defaultValue string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return defaultValue
}

func getEnvInt(key string, defaultValue int) int {
	if value := os.Getenv(key); value != "" {
		if intValue, err := strconv.Atoi(value); err == nil {
			return intValue
		}
	}
	return defaultValue
}

type StatsResponse struct {
	TotalItems     int     `json:"total_items"`
	TotalCategories int    `json:"total_categories"`
	TotalPrice     float64 `json:"total_price"`
}

type PriceRecord struct {
	ID         int
	Name       string
	Category   string
	Price      float64
	CreateDate time.Time
}

var db *sql.DB

func initDB() error {
	dbHost := getEnv("POSTGRES_HOST", "localhost")
	dbPort := getEnvInt("POSTGRES_PORT", 5432)
	dbUser := getEnv("POSTGRES_USER", "validator")
	dbPassword := getEnv("POSTGRES_PASSWORD", "val1dat0r")
	dbName := getEnv("POSTGRES_DB", "project-sem-1")

	psqlInfo := fmt.Sprintf("host=%s port=%d user=%s password=%s dbname=%s sslmode=disable",
		dbHost, dbPort, dbUser, dbPassword, dbName)

	var err error
	db, err = sql.Open("postgres", psqlInfo)
	if err != nil {
		return err
	}

	if err = db.Ping(); err != nil {
		return err
	}

	createTableQuery := `
	CREATE TABLE IF NOT EXISTS prices (
		id INTEGER,
		name VARCHAR(255),
		category VARCHAR(255),
		price DECIMAL(10, 2),
		create_date DATE
	);`

	_, err = db.Exec(createTableQuery)
	if err != nil {
		return err
	}

	return nil
}

func parseCSV(reader io.Reader) ([]PriceRecord, error) {
	csvReader := csv.NewReader(reader)
	records, err := csvReader.ReadAll()
	if err != nil {
		return nil, err
	}

	var priceRecords []PriceRecord
	for i, record := range records {
		if i == 0 {
			continue
		}

		if len(record) < 5 {
			continue
		}

		id, err := strconv.Atoi(strings.TrimSpace(record[0]))
		if err != nil {
			continue
		}

		name := strings.TrimSpace(record[1])
		category := strings.TrimSpace(record[2])

		price, err := strconv.ParseFloat(strings.TrimSpace(record[3]), 64)
		if err != nil {
			continue
		}

		createDate, err := time.Parse("2006-01-02", strings.TrimSpace(record[4]))
		if err != nil {
			continue
		}

		priceRecords = append(priceRecords, PriceRecord{
			ID:         id,
			Name:       name,
			Category:   category,
			Price:      price,
			CreateDate: createDate,
		})
	}

	return priceRecords, nil
}

func extractZipArchive(file io.ReaderAt, size int64) ([]PriceRecord, error) {
	zipReader, err := zip.NewReader(file, size)
	if err != nil {
		return nil, err
	}

	var csvFile *zip.File
	for _, file := range zipReader.File {
		if file.Name == "data.csv" {
			csvFile = file
			break
		}
		if csvFile == nil && strings.HasSuffix(file.Name, ".csv") {
			csvFile = file
		}
	}

	if csvFile == nil {
		return nil, fmt.Errorf("data.csv not found in archive")
	}

	rc, err := csvFile.Open()
	if err != nil {
		return nil, err
	}
	defer rc.Close()

	return parseCSV(rc)
}

func extractTarArchive(file io.Reader) ([]PriceRecord, error) {
	tarReader := tar.NewReader(file)

	var foundCSV bool
	for {
		header, err := tarReader.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}

		if header.Name == "data.csv" {
			return parseCSV(tarReader)
		}
		if !foundCSV && strings.HasSuffix(header.Name, ".csv") {
			foundCSV = true
			return parseCSV(tarReader)
		}
		io.Copy(io.Discard, tarReader)
	}

	return nil, fmt.Errorf("data.csv not found in archive")
}

func insertRecords(records []PriceRecord) error {
	for _, record := range records {
		insertQuery := `
		INSERT INTO prices (id, name, category, price, create_date)
		VALUES ($1, $2, $3, $4, $5);`

		_, err := db.Exec(insertQuery, record.ID, record.Name, record.Category, record.Price, record.CreateDate)
		if err != nil {
			continue
		}
	}

	return nil
}

func getStats() (int, int, float64, error) {
	var totalItems int
	var totalPrice float64
	var totalCategories int

	err := db.QueryRow("SELECT COUNT(*) FROM prices").Scan(&totalItems)
	if err != nil {
		return 0, 0, 0, err
	}

	err = db.QueryRow("SELECT COALESCE(SUM(price), 0) FROM prices").Scan(&totalPrice)
	if err != nil {
		return 0, 0, 0, err
	}

	err = db.QueryRow("SELECT COUNT(DISTINCT category) FROM prices").Scan(&totalCategories)
	if err != nil {
		return 0, 0, 0, err
	}

	return totalItems, totalCategories, totalPrice, nil
}

func handlePostPrices(w http.ResponseWriter, r *http.Request) {
	archiveType := r.URL.Query().Get("type")
	if archiveType == "" {
		archiveType = "zip"
	}

	err := r.ParseMultipartForm(10 << 20)
	if err != nil {
		http.Error(w, "Error parsing form", http.StatusBadRequest)
		return
	}

	file, _, err := r.FormFile("file")
	if err != nil {
		http.Error(w, "Error getting file", http.StatusBadRequest)
		return
	}
	defer file.Close()

	var records []PriceRecord

	if archiveType == "zip" {
		fileBytes, err := io.ReadAll(file)
		if err != nil {
			http.Error(w, "Error reading file", http.StatusInternalServerError)
			return
		}

		readerAt := bytes.NewReader(fileBytes)
		records, err = extractZipArchive(readerAt, int64(len(fileBytes)))
		if err != nil {
			http.Error(w, fmt.Sprintf("Error extracting zip: %v", err), http.StatusBadRequest)
			return
		}
	} else if archiveType == "tar" {
		records, err = extractTarArchive(file)
		if err != nil {
			http.Error(w, fmt.Sprintf("Error extracting tar: %v", err), http.StatusBadRequest)
			return
		}
	} else {
		http.Error(w, "Unsupported archive type", http.StatusBadRequest)
		return
	}

	err = insertRecords(records)
	if err != nil {
		http.Error(w, fmt.Sprintf("Error inserting records: %v", err), http.StatusInternalServerError)
		return
	}

	totalItems, totalCategories, totalPrice, err := getStats()
	if err != nil {
		http.Error(w, fmt.Sprintf("Error getting stats: %v", err), http.StatusInternalServerError)
		return
	}

	response := StatsResponse{
		TotalItems:     totalItems,
		TotalCategories: totalCategories,
		TotalPrice:     totalPrice,
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response)
}

func handleGetPrices(w http.ResponseWriter, r *http.Request) {
	rows, err := db.Query("SELECT id, name, category, price, create_date FROM prices ORDER BY id")
	if err != nil {
		http.Error(w, fmt.Sprintf("Error querying database: %v", err), http.StatusInternalServerError)
		return
	}
	defer rows.Close()

	var records []PriceRecord
	for rows.Next() {
		var record PriceRecord
		err := rows.Scan(&record.ID, &record.Name, &record.Category, &record.Price, &record.CreateDate)
		if err != nil {
			http.Error(w, fmt.Sprintf("Error scanning row: %v", err), http.StatusInternalServerError)
			return
		}
		records = append(records, record)
	}

	tmpFile, err := os.CreateTemp("", "prices-*.zip")
	if err != nil {
		http.Error(w, "Error creating temp file", http.StatusInternalServerError)
		return
	}
	defer os.Remove(tmpFile.Name())
	defer tmpFile.Close()

	zipWriter := zip.NewWriter(tmpFile)
	csvFile, err := zipWriter.Create("data.csv")
	if err != nil {
		http.Error(w, "Error creating csv in zip", http.StatusInternalServerError)
		return
	}

	csvWriter := csv.NewWriter(csvFile)
	csvWriter.Write([]string{"id", "name", "category", "price", "create_date"})

	for _, record := range records {
		csvWriter.Write([]string{
			strconv.Itoa(record.ID),
			record.Name,
			record.Category,
			strconv.FormatFloat(record.Price, 'f', 2, 64),
			record.CreateDate.Format("2006-01-02"),
		})
	}

	csvWriter.Flush()
	zipWriter.Close()

	tmpFile.Seek(0, 0)
	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("Content-Disposition", "attachment; filename=data.zip")
	io.Copy(w, tmpFile)
}

func main() {
	err := initDB()
	if err != nil {
		log.Fatalf("Failed to connect to database: %v", err)
	}
	defer db.Close()

	r := mux.NewRouter()
	r.HandleFunc("/api/v0/prices", handlePostPrices).Methods("POST")
	r.HandleFunc("/api/v0/prices", handleGetPrices).Methods("GET")

	log.Println("Server starting on :8080")
	log.Fatal(http.ListenAndServe(":8080", r))
}
