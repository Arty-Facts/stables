#!/usr/bin/env python3
"""
Test connection to Kokoro TTS server - validates both connectivity and TTS generation.
Use this to verify server is working properly before using the TUI.
"""

import requests
import json
import base64
import io
import soundfile as sf
import argparse
import sys
import time

def test_server(server_url, timeout=30, skip_tts=False):
    """Test server connection and TTS functionality"""
    
    print(f"🔗 Testing connection to: {server_url}")
    
    try:
        # Test 1: Health check
        print("📡 Testing health endpoint...")
        start_time = time.time()
        response = requests.get(f"{server_url}/health", timeout=5)
        
        if response.status_code == 200:
            health_data = response.json()
            print(f"✅ Health check passed: {health_data}")
        else:
            print(f"❌ Health check failed: {response.status_code}")
            return False
            
        if skip_tts:
            print("⏭️ Skipping TTS test (--skip-tts flag)")
            return True
            
        # Test 2: TTS Generation using Kokoro endpoint
        print(f"🎵 Testing Kokoro TTS generation (timeout: {timeout}s)...")
        
        test_payload = {
            "text": "Hello, this is a test of the Kokoro TTS system.",
            "voice": "af_heart", 
            "speed": 1.0
        }
        
        start_time = time.time()
        response = requests.post(
            f"{server_url}/v1/tts",
            json=test_payload,
            timeout=timeout
        )
        generation_time = time.time() - start_time
        
        if response.status_code == 200:
            result = response.json()
            print(f"✅ TTS generation successful")
            print(f"   Generated text: {result.get('text', 'N/A')}")
            print(f"   Voice: {result.get('voice', 'N/A')}")
            print(f"   Speed: {result.get('speed', 'N/A')}")
            
            # Check audio data
            if 'audio' in result and result['audio']:
                audio_length = len(result['audio'])
                sampling_rate = result.get('sampling_rate', 'unknown')
                duration = result.get('duration', 'unknown')
                print(f"   Audio data length: {audio_length} characters (base64)")
                print(f"   Sampling rate: {sampling_rate} Hz")
                print(f"   Duration: {duration} seconds")
                print(f"   Generation time: {generation_time:.2f} seconds")
                
                # Optional: Test audio decoding
                try:
                    audio_bytes = base64.b64decode(result['audio'])
                    audio_io = io.BytesIO(audio_bytes)
                    audio_data, sample_rate = sf.read(audio_io)
                    print(f"   Audio decoded successfully: {len(audio_data)} samples at {sample_rate}Hz")
                except Exception as e:
                    print(f"⚠️  Audio decoding failed: {e}")
                    
            else:
                print("⚠️  No audio data in response")
                
            return True
        else:
            print(f"❌ TTS generation failed: {response.status_code} - {response.text}")
            return False
            
    except requests.exceptions.Timeout:
        print(f"❌ Connection timeout after {timeout} seconds")
        return False
    except requests.exceptions.ConnectionError:
        print(f"❌ Cannot connect to server at {server_url}")
        print("   Make sure the server is running and accessible")
        return False
    except Exception as e:
        print(f"❌ Unexpected error: {e}")
        return False

def main():
    parser = argparse.ArgumentParser(description='Test Kokoro TTS Server Connection')
    parser.add_argument('--server', '-s', 
                       default='http://localhost:17493',
                       help='TTS server URL (default: http://localhost:17493)')
    parser.add_argument('--timeout', '-t', type=int, default=30,
                       help='Timeout for TTS generation test in seconds (default: 30)')
    parser.add_argument('--skip-tts', action='store_true',
                       help='Skip TTS generation test, only test health endpoint')
    
    args = parser.parse_args()
    
    print("🚀 Kokoro TTS Server Connection Test")
    print("=" * 50)
    
    success = test_server(args.server, args.timeout, args.skip_tts)
    
    print("=" * 50)
    if success:
        print("🎉 All tests passed! Kokoro server is working correctly.")
        sys.exit(0)
    else:
        print("💥 Tests failed! Check server status and configuration.")
        sys.exit(1)

if __name__ == "__main__":
    main()
