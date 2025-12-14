#!/bin/bash

set -e

echo "=== Подготовка окружения ==="

# Установка зависимостей Go
echo "Установка зависимостей Go..."
go mod download
go mod tidy

# Проверка наличия PostgreSQL
if ! command -v psql &> /dev/null; then
    echo "PostgreSQL не установлен. Установка PostgreSQL..."
    
    # Определяем ОС и устанавливаем PostgreSQL
    if [[ "$OSTYPE" == "linux-gnu"* ]]; then
        if command -v apt-get &> /dev/null; then
            sudo apt-get update
            sudo apt-get install -y postgresql postgresql-contrib
        elif command -v yum &> /dev/null; then
            sudo yum install -y postgresql postgresql-server
        fi
    elif [[ "$OSTYPE" == "darwin"* ]]; then
        if command -v brew &> /dev/null; then
            brew install postgresql@15
            brew services start postgresql@15
        else
            echo "Ошибка: Homebrew не установлен. Установите PostgreSQL вручную."
            exit 1
        fi
    fi
fi

# Запуск PostgreSQL (если не запущен)
if [[ "$OSTYPE" == "linux-gnu"* ]]; then
    sudo systemctl start postgresql || true
    sudo systemctl enable postgresql || true
elif [[ "$OSTYPE" == "darwin"* ]]; then
    brew services start postgresql@15 || brew services start postgresql || true
fi

# Ожидание запуска PostgreSQL
echo "Ожидание запуска PostgreSQL..."
sleep 3

# Создание пользователя и базы данных
echo "Создание пользователя и базы данных..."

# Определяем команду для подключения к PostgreSQL
if [[ "$OSTYPE" == "darwin"* ]]; then
    # На macOS используем текущего пользователя или postgres через psql
    PSQL_CMD="psql postgres"
    if psql -lqt 2>/dev/null | grep -q "postgres"; then
        PSQL_CMD="psql postgres"
    else
        # Пробуем через пользователя postgres, если он существует
        if id -u postgres >/dev/null 2>&1; then
            PSQL_CMD="sudo -u postgres psql"
        else
            PSQL_CMD="psql postgres"
        fi
    fi
else
    # На Linux используем sudo -u postgres
    PSQL_CMD="sudo -u postgres psql"
fi

# Проверяем, существует ли пользователь
if ! $PSQL_CMD -tAc "SELECT 1 FROM pg_roles WHERE rolname='validator'" 2>/dev/null | grep -q 1; then
    $PSQL_CMD -c "CREATE USER validator WITH PASSWORD 'val1dat0r';" 2>/dev/null || true
fi

# Проверяем, существует ли база данных
if ! $PSQL_CMD -lqt 2>/dev/null | cut -d \| -f 1 | grep -qw "project-sem-1"; then
    $PSQL_CMD -c "CREATE DATABASE \"project-sem-1\" OWNER validator;" 2>/dev/null || true
fi

# Предоставление прав пользователю
$PSQL_CMD -c "GRANT ALL PRIVILEGES ON DATABASE \"project-sem-1\" TO validator;" 2>/dev/null || true

# Создание таблицы
echo "Создание таблицы prices..."
PGPASSWORD=val1dat0r psql -h localhost -U validator -d project-sem-1 -c "
CREATE TABLE IF NOT EXISTS prices (
    id SERIAL PRIMARY KEY,
    name VARCHAR(255) NOT NULL,
    category VARCHAR(255) NOT NULL,
    price DECIMAL(10, 2) NOT NULL,
    create_date TIMESTAMP NOT NULL
);" || true

echo "=== Подготовка завершена ==="
