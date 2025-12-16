#!/bin/bash
# Quick diagnostic script for digital photo frame service

echo "=== Digital Photo Frame Service Diagnostics ==="
echo ""

echo "1. Checking if service is running..."
if systemctl --user is-active --quiet digital-photo-frame.service; then
    echo "   ✓ Service is running"
else
    echo "   ✗ Service is NOT running"
    echo "   Status: $(systemctl --user is-active digital-photo-frame.service)"
fi
echo ""

echo "2. Checking service status..."
systemctl --user status digital-photo-frame.service --no-pager -l | head -20
echo ""

echo "3. Checking if binary exists..."
if [ -f /usr/bin/dpf ]; then
    echo "   ✓ Binary exists at /usr/bin/dpf"
    ls -lh /usr/bin/dpf
else
    echo "   ✗ Binary NOT found at /usr/bin/dpf"
    echo "   Searching for dpf binary..."
    find /usr -name dpf 2>/dev/null | head -5
fi
echo ""

echo "4. Checking if port 8080 is listening..."
if netstat -tln 2>/dev/null | grep -q ":8080 "; then
    echo "   ✓ Port 8080 is listening"
    netstat -tlnp 2>/dev/null | grep ":8080 "
elif ss -tln 2>/dev/null | grep -q ":8080 "; then
    echo "   ✓ Port 8080 is listening"
    ss -tlnp 2>/dev/null | grep ":8080 "
else
    echo "   ✗ Port 8080 is NOT listening"
fi
echo ""

echo "5. Testing local connection..."
if curl -s -o /dev/null -w "%{http_code}" http://localhost:8080 2>/dev/null | grep -q "200\|301\|302"; then
    echo "   ✓ Server responds on localhost:8080"
else
    echo "   ✗ Server does NOT respond on localhost:8080"
fi
echo ""

echo "6. Checking firewall status..."
if command -v ufw >/dev/null 2>&1; then
    ufw status | grep -q "Status: active" && echo "   ⚠ UFW is active - check if port 8080 is allowed" || echo "   ✓ UFW is not active or allows all"
else
    echo "   ℹ UFW not installed"
fi
echo ""

echo "7. Getting network IP addresses..."
hostname -I 2>/dev/null || ip addr show | grep "inet " | grep -v "127.0.0.1" | awk '{print $2}' | cut -d/ -f1
echo ""

echo "8. Recent service logs (last 20 lines)..."
journalctl --user -u digital-photo-frame.service -n 20 --no-pager 2>/dev/null || echo "   Could not retrieve logs"
echo ""

echo "=== Diagnostic Complete ==="
echo ""
echo "To view full logs: journalctl --user -u digital-photo-frame.service -f"
echo "To restart service: systemctl --user restart digital-photo-frame.service"

