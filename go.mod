module github.com/your-org/pki-service

go 1.22

require (
	github.com/go-chi/chi/v5 v5.1.0
	github.com/spf13/viper v1.19.0
	github.com/google/uuid v1.6.0
	github.com/aws/aws-sdk-go-v2 v1.30.0
	github.com/aws/aws-sdk-go-v2/config v1.27.0
	github.com/aws/aws-sdk-go-v2/service/s3 v1.58.0
	github.com/redis/go-redis/v9 v9.6.0
	github.com/rabbitmq/amqp091-go v1.10.0
	go.uber.org/zap v1.27.0
	github.com/stretchr/testify v1.9.0
	github.com/pdfcpu/pdfcpu v0.8.0
	github.com/skip2/go-qrcode v0.0.0-20200617195104-da1b6568686e
	github.com/golang-migrate/migrate/v4 v4.17.1
	github.com/lib/pq v1.10.9
	github.com/golang-jwt/jwt/v5 v5.2.1
	github.com/stretchr/mock v0.0.0-20200221133545-7c8b3bab6aae
)
