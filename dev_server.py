"""CAS standalone dev server — run with: python dev_server.py

Starts CAS on port 8301 without requiring Heddle.
Set CAS_PROVIDER and ANTHROPIC_API_KEY before running if using the Anthropic backend.
"""
import sys
from pathlib import Path

sys.path.insert(0, str(Path(__file__).parent / "src"))

from fastapi import FastAPI
from fastapi.middleware.cors import CORSMiddleware
from cas.api import create_router

app = FastAPI(title="CAS — Conversational Agent Shell")
app.add_middleware(CORSMiddleware, allow_origins=["*"], allow_methods=["*"], allow_headers=["*"])
app.include_router(create_router())

if __name__ == "__main__":
    import uvicorn
    import os
    port = int(os.environ.get("CAS_PORT", 8301))
    print(f"CAS running at http://localhost:{port}/api/cas/")
    uvicorn.run(app, host="0.0.0.0", port=port)
