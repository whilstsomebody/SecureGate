# SecureGate — API Gateway in Golang

SecureGate is a production-style **API Gateway** built in **Golang**, 
designed to secure and manage microservice traffic.  
It acts as a centralized entry point for backend services and provides key 
features like authentication, authorization, rate limiting, and monitoring.

*This project showcases real-world backend engineering and DevOps practices.*

---

## Functionalities

SecureGate includes the following core features:

- **Reverse Proxy Routing** to multiple backend microservices  
- **JWT Authentication** for secure API access  
- **Role-Based Access Control (RBAC)** with USER / ADMIN protected routes  
- **Redis-backed Rate Limiting** to prevent abuse and control traffic  
- **Prometheus Metrics Endpoint** for monitoring request activity  
- **Grafana Dashboard Support** for observability and performance tracking  

---

## Tech Stack

- Golang  
- Redis  
- Prometheus  
- Grafana  
- Docker Compose  

---

## Project Structure

```bash

securegate/
┣ cmd/
┃ ┣ gateway/ # Main API Gateway
┃ ┣ userservice/ # Sample User Service
┃ ┣ adminservice/ # Sample Admin Service
┃ ┗ token/ # JWT Token Generator
┣ internal/
┃ ┣ proxy/ # Reverse proxy routing logic
┃ ┣ middleware/ # Auth, RBAC, Rate Limiting, Metrics
┃ ┣ ratelimit/ # Redis limiter implementation
┃ ┗ metrics/ # Prometheus metrics definitions
┣ configs/
┃ ┗ prometheus.yml
┣ docker-compose.yml
┗ README.md

```

## Setup and Run Instructions

### 1. Clone the Repository

```bash
git clone https://github.com/yourusername/securegate.git
cd securegate
```

### 2. Configure Environment Variables

Create a `.env` file in the project root:

```bash
JWT_SECRET=my_ultra_secure_secret_key
REDIS_ADDR=127.0.0.1:6379
```

### 3. Start Redis + Monitoring Stack

Run the observability services:

```bash
docker compose up -d
```

This starts:

- Redis
- Prometheus
- Grafana

### 4. Run Backend Microservices

Start the User Service:

```bash
go run cmd/userservice/main.go
```

Start the Admin Service:

```bash
go run cmd/adminservice/main.go
```
### 5. Run SecureGate Gateway

Start the API Gateway:

```bash
go run cmd/gateway/main.go
```

Gateway runs on `http://localhost:8080`.

---

## Authentication Usage

a. Generate a JWT Token

```bash
go run cmd/token/main.go ADMIN
```
Copy the token output.

b. Call a Protected API Route

```bash
curl -H "Authorization: Bearer <TOKEN>" \
http://localhost:8080/users/hello
```

---

## Rate Limiting Test

Send multiple requests quickly:

```bash
for i in {1..10}; do
  curl -H "Authorization: Bearer <TOKEN>" \
  http://localhost:8080/users/hello
done
```
After the limit is exceeded:

```bash
429 Too Many Requests
```

---

## Monitoring and Metrics

### Prometheus Dashboard

Prometheus Metrics Endpoint: `http://localhost:8080/metrics`

Prometheus UI: `http://localhost:9090`

### Grafana Dashboard

Open Grafana UI: `http://localhost:3000`

Login: Username - `admin`, Password - `admin`

Add Prometheus data source: `http://prometheus:9090`

<img width="1536" height="1024" alt="grafana_prometheus" src="https://github.com/user-attachments/assets/61ca484d-b61f-4cbc-a9aa-21579f12a052" />

---

## Architecture 

SecureGate is designed as a centralized **API Gateway layer** that sits between 
clients and backend microservices. It provides security, traffic control, and 
observability features before forwarding requests to internal services.

```sql

          Client Request
                 |
                 v
 +------------------------------+
 |       SecureGate Gateway     |
 |------------------------------|
 |                              |
 |     JWT Authentication       |
 |     RBAC Authorization       |
 |     Redis Rate Limiting      |
 |     Metrics Middleware       |
 +------------------------------+
    |            |           |
    v            v           v
User Service Admin Service Other Services
                 |
                 v
+--------------------------------+
|    Prometheus Metrics Store    |
+--------------------------------+
                 |
                 v
+--------------------------------+
|      Grafana Dashboard UI      |
+--------------------------------+

```

## Contribution

Solely by me. But if you want to, open a PR. 
