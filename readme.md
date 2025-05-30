# Docker MTSD Load Balancer

A distributed load balancer implementation with consistent hashing, health checking, and Docker containerization support.

## 📋 Overview

This application implements a load balancer that distributes HTTP requests across multiple backend servers using consistent hashing for sticky routing. The system includes health monitoring, request tracing, and comprehensive testing.

**📖 Design Document:** [Load Balancer Design Document](https://docs.google.com/document/d/1pQJ2rSgXwHG0egyCliCjX4JKHByapvCpEZd0MlATlQQ/edit?tab=t.0)

## 🏗️ Architecture

The application consists of several components:

- **Load Balancer** (`cmd/lb/`): Routes requests using consistent hashing with health checking
- **Backend Servers** (`cmd/server/`): HTTP servers that handle API requests and health checks
- **Client** (`cmd/client/`): Test client for making requests to the load balancer
- **Stats Collector** (`cmd/stats/`): Utility for collecting and displaying server statistics

### Key Features

- **Consistent Hashing**: Ensures the same URL path always routes to the same healthy server
- **Health Monitoring**: Automatic health checks every 10 seconds with failover
- **Request Tracing**: Optional tracing headers to track which server handled each request
- **Configurable Timeouts**: Adjustable request timeouts and response delays
- **Docker Support**: Full containerization with Docker Compose
- **Integration Testing**: Comprehensive test suite including load testing

## 🚀 Quick Start

### Prerequisites

- Docker and Docker Compose
- Go 1.24+ (for local development)

### Running with Docker Compose

1. **Build and start all services:**

   ```bash
   docker compose up --build
   ```

2. **The load balancer will be available at:** `http://localhost:8090`

3. **Test the setup:**
   ```bash
   curl http://localhost:8090/api/v1/some-data
   ```

### Services and Ports

| Service       | Port | Description             |
| ------------- | ---- | ----------------------- |
| Load Balancer | 8090 | Main entry point        |
| Server 1      | 8080 | Backend server instance |
| Server 2      | 8081 | Backend server instance |
| Server 3      | 8082 | Backend server instance |

## 🛠️ Building and Running

### Local Development

1. **Build all binaries:**

   ```bash
   go build ./cmd/lb
   go build ./cmd/server
   go build ./cmd/client
   go build ./cmd/stats
   ```

2. **Run load balancer:**

   ```bash
   ./lb -port=8090 -trace=true
   ```

3. **Run backend servers:**

   ```bash
   ./server -port=8080  # Terminal 1
   ./server -port=8081  # Terminal 2
   ./server -port=8082  # Terminal 3
   ```

4. **Run test client:**
   ```bash
   ./client -target=http://localhost:8090
   ```

### Docker Build

```bash
# Build the Docker image
docker build -t docker-mtsd .

# Run load balancer
docker run -p 8090:8090 docker-mtsd lb

# Run server
docker run -p 8080:8080 docker-mtsd server
```

## 🧪 Testing

### Unit Tests

```bash
# Run all unit tests
go test ./...

# Run tests with coverage
go test -cover ./...

# Run specific package tests
go test ./cmd/lb
go test ./cmd/server
```

### Integration Tests

```bash
# Using Docker Compose
docker compose -f docker-compose.yaml -f docker-compose.test.yaml up --exit-code-from test

# The integration tests will:
# - Verify all servers are healthy
# - Test load distribution across servers
# - Validate consistent hashing behavior
# - Test URL sticky routing
# - Run performance benchmarks
```

### Manual Testing

1. **Health Check:**

   ```bash
   curl http://localhost:8080/health
   curl http://localhost:8081/health
   curl http://localhost:8082/health
   ```

2. **Load Balancer Endpoints:**

   ```bash
   curl -v http://localhost:8090/api/v1/some-data
   curl -v http://localhost:8090/api/v1/some-data-check1
   curl -v http://localhost:8090/api/v1/some-data-check2
   curl -v http://localhost:8090/api/v1/some-data-check3
   ```

3. **Check Server Reports:**

   ```bash
   curl http://localhost:8080/report
   curl http://localhost:8081/report
   curl http://localhost:8082/report
   ```

4. **Collect Statistics:**
   ```bash
   ./stats
   ```

## ⚙️ Configuration

### Load Balancer Options

```bash
./lb -h
```

| Flag           | Default | Description                         |
| -------------- | ------- | ----------------------------------- |
| `-port`        | 8090    | Load balancer port                  |
| `-timeout-sec` | 3       | Request timeout in seconds          |
| `-https`       | false   | Use HTTPS for backend communication |
| `-trace`       | false   | Enable tracing headers              |

### Server Configuration

Environment variables for server behavior:

| Variable                  | Description                       | Example |
| ------------------------- | --------------------------------- | ------- |
| `CONF_RESPONSE_DELAY_SEC` | Add artificial delay to responses | `2`     |
| `CONF_HEALTH_FAILURE`     | Force health check failures       | `true`  |

### Example with Custom Configuration

```bash
# Run with tracing enabled and custom timeout
./lb -port=8090 -timeout-sec=5 -trace=true

# Run server with 2-second response delay
CONF_RESPONSE_DELAY_SEC=2 ./server -port=8080

# Run server that fails health checks
CONF_HEALTH_FAILURE=true ./server -port=8081
```

## 🔍 API Endpoints

### Load Balancer

- `GET /api/v1/some-data` - Returns `["1", "2"]`
- `GET /api/v1/some-data-check1` - Returns `["2", "3"]`
- `GET /api/v1/some-data-check2` - Returns `["3", "4"]`
- `GET /api/v1/some-data-check3` - Returns `["4", "5"]`

### Backend Servers

- `GET /health` - Health check endpoint
- `GET /report` - Request statistics per client
- `GET /api/v1/*` - Data endpoints (same as load balancer)

### Response Headers

When tracing is enabled (`-trace=true`):

- `lb-from: server1:8080` - Indicates which backend server handled the request

## 🔧 CI/CD

The project includes GitHub Actions workflow (`.github/workflows/go.yml`):

- **Triggers**: Push and pull requests on all branches
- **Steps**:
  1. Checkout repository
  2. Set up Docker Buildx
  3. Build Docker image and run unit tests
  4. Run integration tests

```bash
# Locally run the same CI steps
docker compose build
docker compose -f docker-compose.yaml -f docker-compose.test.yaml up --exit-code-from test
```

## 🐛 Troubleshooting

### Common Issues

1. **Port conflicts:**

   ```bash
   # Check if ports are in use
   lsof -i :8090
   lsof -i :8080-8082
   ```

2. **Docker network issues:**

   ```bash
   # Clean up Docker resources
   docker compose down
   docker system prune -f
   ```

3. **Health check failures:**
   ```bash
   # Check server logs
   docker compose logs server1
   docker compose logs server2
   docker compose logs server3
   ```

### Debug Mode

Enable verbose logging:

```bash
./lb -trace=true  # Enable request tracing
./client -target=http://localhost:8090  # Watch client requests
```

## 📁 Project Structure

```
.
├── cmd/
│   ├── client/          # Test client
│   ├── lb/              # Load balancer
│   ├── server/          # Backend server
│   └── stats/           # Statistics collector
├── httptools/           # HTTP server utilities
├── integration/         # Integration tests
├── signal/              # Signal handling utilities
├── docker-compose.yaml  # Main Docker Compose
├── docker-compose.test.yaml  # Test configuration
├── Dockerfile           # Main application image
├── Dockerfile.test      # Test image
└── entry.sh            # Container entry point
```

## 📝 Development

### Adding New Endpoints

1. Add endpoint to `cmd/server/server.go`
2. Update integration tests in `integration/balancer_test.go`
3. Test with the client or curl

### Modifying Load Balancing Logic

1. Update `chooseServer()` function in `cmd/lb/balancer.go`
2. Add corresponding unit tests in `cmd/lb/balancer_test.go`
3. Run integration tests to verify behavior

## 📄 License

This project is part of a distributed systems learning exercise.
