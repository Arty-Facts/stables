#!/bin/bash
# Quick Kokoro TTS Server Starter

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
ROOT_DIR="$SCRIPT_DIR/.."

# Colors
GREEN='\033[0;32m'
BLUE='\033[0;34m'
YELLOW='\033[1;33m'
RED='\033[0;31m'
NC='\033[0m'

echo -e "${GREEN}🚀 Starting Kokoro TTS Server...${NC}"

# Check if virtual environment exists
if [ ! -d "$ROOT_DIR/tts_venv" ]; then
    echo -e "${RED}❌ Virtual environment not found!${NC}"
    echo -e "Run from project root: ${YELLOW}python -m venv tts_venv && pip install -r backend/requirements.txt${NC}"
    exit 1
fi

# Activate environment
source "$ROOT_DIR/tts_venv/bin/activate"

echo -e "${BLUE}🔧 Environment activated${NC}"

# Check and download Kokoro model files if they don't exist
echo -e "${BLUE}📦 Checking Kokoro model files...${NC}"

mkdir -p "$ROOT_DIR/models"
MODEL_FILE="$ROOT_DIR/models/kokoro-v1.0.onnx"
VOICES_FILE="$ROOT_DIR/models/voices-v1.0.bin"
MODEL_URL="https://github.com/thewh1teagle/kokoro-onnx/releases/download/model-files-v1.0/kokoro-v1.0.onnx"
VOICES_URL="https://github.com/thewh1teagle/kokoro-onnx/releases/download/model-files-v1.0/voices-v1.0.bin"

# Check and download model file
if [ ! -f "$MODEL_FILE" ]; then
    echo -e "${YELLOW}📥 Downloading Kokoro model (~310MB)...${NC}"
    wget -O "$MODEL_FILE" "$MODEL_URL"
    if [ $? -eq 0 ]; then
        echo -e "${GREEN}✅ Model downloaded successfully${NC}"
    else
        echo -e "${RED}❌ Failed to download model file${NC}"
        exit 1
    fi
else
    echo -e "${GREEN}✅ Model file found: $MODEL_FILE${NC}"
fi

# Check and download voices file
if [ ! -f "$VOICES_FILE" ]; then
    echo -e "${YELLOW}📥 Downloading Kokoro voices (~27MB)...${NC}"
    wget -O "$VOICES_FILE" "$VOICES_URL"
    if [ $? -eq 0 ]; then
        echo -e "${GREEN}✅ Voices downloaded successfully${NC}"
    else
        echo -e "${RED}❌ Failed to download voices file${NC}"
        exit 1
    fi
else
    echo -e "${GREEN}✅ Voices file found: $VOICES_FILE${NC}"
fi

echo -e "${BLUE}🤖 Starting Kokoro TTS Server...${NC}"

# Install psutil if not present (needed for server management)
if ! python -c "import psutil" 2>/dev/null; then
    echo -e "${YELLOW}📦 Installing psutil for server management...${NC}"
    pip install psutil>=5.8.0
fi

echo ""
echo -e "${YELLOW}Server will be available at: http://localhost:17493${NC}"
echo -e "${YELLOW}Health check: http://localhost:17493/health${NC}"
echo ""
echo -e "${BLUE}To connect from another machine via SSH tunnel:${NC}"
echo -e "  ${YELLOW}ssh -L 17493:localhost:17493 $(whoami)@$(hostname -I | awk '{print $1}')${NC}"
echo ""
echo -e "${RED}Press Ctrl+C to stop the server${NC}"
echo ""

# Start the server (it will automatically kill any existing servers)
python "$SCRIPT_DIR/start_server.py" --host localhost --port 17493
