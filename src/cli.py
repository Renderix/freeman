import click
from .server import start_server
from .config import config

@click.group()
def cli():
    """Freeman TTS - High-performance streaming TTS server."""
    pass

@cli.command()
@click.option('--port', default=17000, help='Port to start the WebSocket server on.')
def start(port):
    """Start the WebSocket TTS server."""
    click.echo(f"Starting Freeman server on port {port}...")
    start_server(port)

@cli.command()
@click.option('--port', default=8000, help='Port to start the setup UI on.')
def setup(port):
    """Start the configuration web UI."""
    click.echo(f"Starting Freeman setup UI on port {port}...")
    # NOTE: In Phase 1 we use a simple setup server or integrate it into server.py
    # For now, let's keep it simple.
    from fastapi import FastAPI
    from fastapi.responses import HTMLResponse
    import uvicorn
    import os
    
    setup_app = FastAPI()
    
    @setup_app.get("/", response_class=HTMLResponse)
    async def get_setup():
        setup_path = os.path.join(os.getcwd(), "static", "setup.html")
        with open(setup_path, "r") as f:
            return f.read()
            
    # Add endpoints for saving config etc.
    @setup_app.get("/config")
    async def get_config():
        return config.settings
        
    @setup_app.post("/config")
    async def update_config(settings: dict):
        for k, v in settings.items():
            config.set(k, v)
        return {"status": "success"}

    uvicorn.run(setup_app, host="0.0.0.0", port=port)

@cli.command()
def version():
    """Show version info."""
    click.echo("Freeman TTS v1.0.0-draft")

if __name__ == "__main__":
    cli()
