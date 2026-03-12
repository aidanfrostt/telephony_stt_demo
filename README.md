<div align="center">
  
  # 📞 Standalone Telephony / STT Gateway
  **High-Concurrency WebSocket Bridge & Live Tracking Dashboard**

  [![Go](https://img.shields.io/badge/Built%20With-Go-00ADD8?style=for-the-badge&logo=go&logoColor=white)](https://go.dev/)
  [![WebSockets](https://img.shields.io/badge/Powered%20By-WebSockets-black?style=for-the-badge&logo=socket.io)](https://developer.mozilla.org/en-US/docs/Web/API/WebSockets_API)
  [![Deepgram](https://img.shields.io/badge/STT-Deepgram-4f46e5?style=for-the-badge)](https://deepgram.com/)
  [![Twilio](https://img.shields.io/badge/Media%20Streams-Twilio-F22F46?style=for-the-badge&logo=twilio)](https://twilio.com/)
  <br />
</div>

<hr />

## 🌟 Overview
This repository provides a highly-optimized, incredibly fast standalone HTTP/WebSocket gateway written entirely in **Go**. It acts as a bridge between **Twilio Media Streams** (receiving live phone call audio) and **Deepgram's Live Transcription** engine, converting bidirectional binary audio cleanly.

It was designed specifically to handle **hundreds of concurrent lines** with minimal overhead, replacing the standard Node.js event-loop bottlenecks with Go's lightweight routines and aggressive Memory/Garbage Collection limits.

### ✨ Features
- **Zero-Block Concurrency**: Every phone call spins up non-blocking Goroutines.
- **Sync.Pool Optimization**: Instead of reallocating byte arrays for the thousands of 160-byte payload chunks coming in per second, we utilize a strictly bound `sync.Pool` to heavily suppress Garbage Collector execution and memory leaks.
- **Sleek Real-time Tracking**: Includes a breathtaking "Quiet Luxury" Dashboard hosted directly from the Go process indicating Active Sessions and Live Transcripts safely synced via `sync.RWMutex`.

---

## 🎨 The Dashboard
This project natively hosts a glassmorphism real-time analytics dashboard accessible at `/ui`.

No React. No Webpack. Just pure, highly stylized vanilla DOM manipulation driven directly by Go web sockets tracking your global active calls, traffic totals, and an ongoing real-time transcript stream.

---

## 🚀 Deployment Architecture

Because streaming Media from Twilio to Deepgram requires persistent, alive WebSocket connections, **you cannot host the Go Backend on Serverless platforms** (e.g. Vercel / Netlify Functions).

### 1. Hosting the Go Engine (Backend)
Deploy this repository to a persistent container/VPS host like:
- **Render.com** 
- **Fly.io**
- **Railway.app**

**Environment Variables Required:**
```env
DEEPGRAM_API_KEY=your_key_here
PORT=8081 # (Handled automatically by most hosts)
```

### 2. Hosting the UI (Netlify)
While the backend must run persistently, you CAN deploy the UI to a static host! We have provided a `netlify.toml`.
1. Link this Repo to **Netlify**.
2. It will detect the `public` directory and deploy the interface instantly.
3. Access your static UI and append `?server=` as a query param pointing to your persistent Go backend to view live traffic magically.
   > **Example:** `https://your-ui.netlify.app/?server=wss://your-api.fly.dev/ui-ws`

---

## 💻 Local Development
1. Clone the repository natively.
2. Initialize deps: `go mod tidy`
3. Make sure you have `.env` containing your `DEEPGRAM_API_KEY`.
4. Run the engine: `go run .`
5. Open `http://localhost:8081/ui`

Hook up your Twilio Application's *Media Stream wss:// URL* to your local ngrok, and you're off!
