#!/usr/bin/env python3
"""
Simple test script to verify clipboard and TTS functionality
"""

import requests
import pyperclip
import time

def test_server_connection():
    """Test basic server connection"""
    try:
        response = requests.get("http://localhost:17493/health", timeout=5)
        if response.status_code == 200:
            print("✅ Server connection successful")
            return True
        else:
            print(f"❌ Server returned status {response.status_code}")
            return False
    except Exception as e:
        print(f"❌ Server connection failed: {e}")
        return False

def test_tts_generation():
    """Test basic TTS generation"""
    try:
        payload = {
            "text": "Hello, this is a test message for the TTS system. A bronze fox jumps over the lazy dog.",
            "voice": "af_heart", 
            "speed": 1.0
        }
        
        response = requests.post("http://localhost:17493/v1/tts", json=payload, timeout=30)
        
        if response.status_code == 200:
            result = response.json()
            print(f"✅ TTS generation successful: {result['duration']:.2f}s audio")
            return True
        else:
            print(f"❌ TTS generation failed: {response.status_code}")
            return False
    except Exception as e:
        print(f"❌ TTS generation error: {e}")
        return False

def test_clipboard():
    """Test clipboard functionality"""
    try:
        # Test setting clipboard
        test_text = "This is a clipboard test message. A quick brown fox jumps over the lazy dog."
        pyperclip.copy(test_text)
        
        # Test reading clipboard
        clipboard_content = pyperclip.paste()
        
        if clipboard_content == test_text:
            print("✅ Clipboard functionality working")
            return True
        else:
            print(f"❌ Clipboard test failed: expected '{test_text}', got '{clipboard_content}'")
            return False
    except Exception as e:
        print(f"❌ Clipboard error: {e}")
        return False

def main():
    print("🧪 Testing TTS System Components")
    print("=" * 40)
    
    # Test 1: Server connection
    print("1. Testing server connection...")
    server_ok = test_server_connection()
    
    # Test 2: TTS generation
    if server_ok:
        print("\n2. Testing TTS generation...")
        tts_ok = test_tts_generation()
    else:
        tts_ok = False
    
    # Test 3: Clipboard
    print("\n3. Testing clipboard...")
    clipboard_ok = test_clipboard()
    
    # Summary
    print("\n" + "=" * 40)
    print("📊 Test Results:")
    print(f"Server Connection: {'✅' if server_ok else '❌'}")
    print(f"TTS Generation: {'✅' if tts_ok else '❌'}")
    print(f"Clipboard: {'✅' if clipboard_ok else '❌'}")
    
    if server_ok and tts_ok and clipboard_ok:
        print("\n🎉 All tests passed! TTS TUI should work correctly.")
        print("\n💡 To test clipboard TTS:")
        print("   1. Run: python tts_tui.py")
        print("   2. Copy some text to clipboard")
        print("   3. Audio should generate automatically")
    else:
        print("\n⚠️ Some tests failed. Check the issues above.")
    
    return server_ok and tts_ok and clipboard_ok

if __name__ == "__main__":
    main()