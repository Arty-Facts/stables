#!/bin/bash
# TTS Virtual Environment Activation Script

# Colors for output
GREEN='\033[0;32m'
BLUE='\033[0;34m'
YELLOW='\033[1;33m'
NC='\033[0m' # No Color

echo -e "${GREEN}🎤 Activating TTS Environment...${NC}"

# Get the directory where this script is located
SCRIPT_DIR="$( cd "$( dirname "${BASH_SOURCE[0]}" )" &> /dev/null && pwd )"

# Activate the virtual environment
source "$SCRIPT_DIR/tts_venv/bin/activate"

echo -e "${GREEN}✓ Virtual environment activated${NC}"
echo -e "${BLUE}Available commands:${NC}"
echo -e "  ${YELLOW}python TTS_TUI.py${NC}           - Start the TTS Terminal UI"
echo -e "  ${YELLOW}python test_connection.py${NC}   - Test server connectivity" 
echo -e "  ${YELLOW}python start_server.py${NC}      - Start the TTS server (on this machine)"
echo -e "  ${YELLOW}ssh -L 17493:localhost:17493 user@server${NC} - SSH tunnel"
echo ""
echo -e "${BLUE}Quick start with SSH tunnel:${NC}"
echo -e "  ${YELLOW}ssh tts-server${NC}  # (if configured in ~/.ssh/config)"
echo -e "  ${YELLOW}./test_connection.py${NC}"
echo -e "  ${YELLOW}python TTS_TUI.py${NC}"
echo ""
echo -e "${BLUE}Environment info:${NC}"
echo -e "  Python: $(python --version)"
echo -e "  Virtual env: $VIRTUAL_ENV"
echo ""
