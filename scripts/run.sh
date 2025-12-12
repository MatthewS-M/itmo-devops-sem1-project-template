#!/bin/bash

set -e

# Проверка наличия переменных окружения для SSH
if [ -z "$SSH_HOST" ] || [ -z "$SSH_USER" ]; then
    echo "=== Локальный запуск приложения ==="
    
    # Запуск PostgreSQL локально
    if [[ "$OSTYPE" == "linux-gnu"* ]]; then
        sudo systemctl start postgresql || true
    elif [[ "$OSTYPE" == "darwin"* ]]; then
        brew services start postgresql@15 || true
    fi
    
    # Ожидание запуска PostgreSQL
    echo "Ожидание запуска PostgreSQL..."
    sleep 3
    
    # Запуск приложения в фоне
    echo "Запуск приложения на localhost:8080..."
    go run main.go &
    APP_PID=$!
    
    echo "Приложение запущено с PID: $APP_PID"
    echo "Сервер доступен по адресу: http://localhost:8080"
    echo "Для остановки приложения выполните: kill $APP_PID"
    
    # Сохранение PID в файл для последующей остановки
    echo $APP_PID > .app.pid
    
    exit 0
fi

# === Удаленный деплой через SSH ===
echo "=== Удаленный деплой приложения ==="

SSH_KEY="${SSH_KEY:-}"
SSH_PORT="${SSH_PORT:-22}"

# Формирование команды SSH
SSH_CMD="ssh"
if [ -n "$SSH_KEY" ]; then
    SSH_CMD="$SSH_CMD -i $SSH_KEY"
fi
SSH_CMD="$SSH_CMD -p $SSH_PORT -o StrictHostKeyChecking=no"

# Подключение к серверу и выполнение команд
echo "Подключение к серверу $SSH_USER@$SSH_HOST..."

# Создание директории для проекта на удаленном сервере
$SSH_CMD $SSH_USER@$SSH_HOST "mkdir -p ~/project-sem-1"

# Копирование файлов проекта на сервер
echo "Копирование файлов проекта..."
if [ -n "$SSH_KEY" ]; then
    scp -i $SSH_KEY -P $SSH_PORT -o StrictHostKeyChecking=no -r \
        main.go go.mod go.sum scripts/ $SSH_USER@$SSH_HOST:~/project-sem-1/
else
    scp -P $SSH_PORT -o StrictHostKeyChecking=no -r \
        main.go go.mod go.sum scripts/ $SSH_USER@$SSH_HOST:~/project-sem-1/
fi

# Выполнение команд на удаленном сервере
echo "Установка зависимостей и запуск приложения на удаленном сервере..."
$SSH_CMD $SSH_USER@$SSH_HOST << 'ENDSSH'
    cd ~/project-sem-1
    
    # Установка Go (если не установлен)
    if ! command -v go &> /dev/null; then
        echo "Установка Go..."
        wget -q https://go.dev/dl/go1.23.3.linux-amd64.tar.gz
        sudo rm -rf /usr/local/go
        sudo tar -C /usr/local -xzf go1.23.3.linux-amd64.tar.gz
        export PATH=$PATH:/usr/local/go/bin
        echo 'export PATH=$PATH:/usr/local/go/bin' >> ~/.bashrc
    fi
    
    # Установка PostgreSQL (если не установлен)
    if ! command -v psql &> /dev/null; then
        echo "Установка PostgreSQL..."
        sudo apt-get update
        sudo apt-get install -y postgresql postgresql-contrib
    fi
    
    # Запуск PostgreSQL
    sudo systemctl start postgresql
    sudo systemctl enable postgresql
    sleep 3
    
    # Создание пользователя и базы данных
    sudo -u postgres psql -c "CREATE USER validator WITH PASSWORD 'val1dat0r';" 2>/dev/null || true
    sudo -u postgres psql -c "CREATE DATABASE \"project-sem-1\" OWNER validator;" 2>/dev/null || true
    sudo -u postgres psql -c "GRANT ALL PRIVILEGES ON DATABASE \"project-sem-1\" TO validator;" 2>/dev/null || true
    
    # Установка зависимостей Go
    export PATH=$PATH:/usr/local/go/bin
    go mod download
    go mod tidy
    
    # Остановка предыдущего экземпляра приложения (если запущен)
    pkill -f "go run main.go" || true
    pkill -f "./main" || true
    
    # Компиляция и запуск приложения в фоне
    go build -o main main.go
    nohup ./main > app.log 2>&1 &
    
    echo "Приложение запущено"
    
    # Получение IP-адреса сервера
    SERVER_IP=$(curl -s ifconfig.me || curl -s icanhazip.com || hostname -I | awk '{print $1}')
    echo "IP-адрес сервера: $SERVER_IP"
ENDSSH

# Получение IP-адреса сервера
SERVER_IP=$($SSH_CMD $SSH_USER@$SSH_HOST "curl -s ifconfig.me || curl -s icanhazip.com || hostname -I | awk '{print \$1}'")

echo "=== Деплой завершен ==="
echo "IP-адрес сервера: $SERVER_IP"
echo "$SERVER_IP"
