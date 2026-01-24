# AIBlackBox 🛡️🤖
### Immutable Audit Trail for LLM Applications

**AIBlackBox** is a high-performance, transparent proxy built in Go, designed to act as a "Flight Recorder" for your AI interactions. It ensures that every request and response between your application and LLM providers (OpenAI, Azure, Anthropic, Local models) is cryptographically signed, chained, and stored for compliance and auditing.



## ✨ Key Features
- **Transparent Proxying:** No code changes required. Just point your OpenAI-compatible SDK to AIBlackBox.
- **Named Endpoints:** Route requests to multiple providers through a single secure gateway.
- **Cryptographic Integrity:** Uses SHA-256 hash-chaining ($H_n = \text{hash}(\text{data}_n + H_{n-1})$) to ensure logs are tamper-proof.
- **Async Processing:** Near-zero latency overhead thanks to Go's concurrency and non-blocking architecture.
- **Streaming Support:** Full support for Server-Sent Events (SSE) while capturing complete logs.
- **Enterprise Ready:** Lightweight Docker image, environment variable configuration, and local volume support.

## 🚀 Quick Start

### 1. Configuration
Create a `config.yaml` to define your endpoints:

```yaml
server:
  port: 8080
  genesis_seed: "firm-exclusive-secret-seed"

endpoints:
  - name: "production"
    target: "[https://api.openai.com/v1](https://api.openai.com/v1)"
  - name: "research"
    target: "http://internal-llm-server:11434/v1"

storage:
  path: "./logs/audit.jsonl"
```

### 2. Run with Docker
``` Bash

docker run -p 8080:8080 \
  -v $(pwd)/config.yaml:/app/config.yaml \
  -v $(pwd)/logs:/app/logs \
  aiblackbox/proxy:latest
```

### 3. Integration
Simply change the base_url in your application's SDK.

Python Example:

``` Python

from openai import OpenAI

client = OpenAI(
    api_key="your-api-key",
    base_url="http://localhost:8080/production"
)
```