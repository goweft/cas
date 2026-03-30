"""CAS dev server — run with: python dev_server.py"""
import sys
from pathlib import Path

sys.path.insert(0, str(Path(__file__).parent / "src"))
sys.path.insert(0, str(Path.home() / "projects" / "loom" / "src"))

from fastapi import FastAPI
from cas.api import create_router

app = FastAPI(title="CAS Dev")
app.include_router(create_router())

if __name__ == "__main__":
    import uvicorn
    uvicorn.run(app, host="0.0.0.0", port=8301)
