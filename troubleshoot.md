# Troubleshooting Raspberry Pi Connection Issues

If you can't access the web server at `ouyang-photo-frame:8080`, check the following:

## 1. Check if the service is running

```bash
# Check service status
systemctl --user status digital-photo-frame.service

# Or if running as system service
sudo systemctl status digital-photo-frame.service
```

## 2. Check service logs

```bash
# View recent logs
journalctl --user -u digital-photo-frame.service -n 50

# Or if running as system service
sudo journalctl -u digital-photo-frame.service -n 50

# Follow logs in real-time
journalctl --user -u digital-photo-frame.service -f
```

## 3. Verify the binary exists and is executable

```bash
# Check if binary exists
ls -la /usr/bin/dpf

# If not, check where it is
which dpf

# Make sure it's executable
sudo chmod +x /usr/bin/dpf
```

## 4. Test if the server is listening on port 8080

```bash
# Check if port 8080 is in use
sudo netstat -tlnp | grep 8080
# or
sudo ss -tlnp | grep 8080

# Try connecting locally
curl http://localhost:8080
curl http://127.0.0.1:8080
```

## 5. Check firewall settings

```bash
# Check if ufw is blocking port 8080
sudo ufw status

# If ufw is active, allow port 8080
sudo ufw allow 8080/tcp
sudo ufw reload
```

## 6. Verify environment variables

```bash
# Check service environment variables
systemctl --user show digital-photo-frame.service | grep Environment

# Test running the binary manually with environment variables
export DPF_ROOT_PATH=/path/to/your/photos
export DPF_AWS_PROFILE=your-profile
export DPF_S3_BUCKET=your-bucket
export DPF_TARGET_MAX_DIM=1024
export XDG_RUNTIME_DIR=/run/user/1000
/usr/bin/dpf
```

## 7. Check network connectivity

```bash
# Get the Pi's IP address
hostname -I
# or
ip addr show

# Try accessing from the Pi itself
curl http://$(hostname -I | awk '{print $1}'):8080

# Try accessing by IP instead of hostname
# Use the IP from hostname -I instead of ouyang-photo-frame
```

## 8. Verify service file configuration

Make sure your `digital-photo-frame.service` file has:
- Correct `ExecStart` path
- All required environment variables set
- Correct `WantedBy` target

## 9. Restart the service

```bash
# Stop the service
systemctl --user stop digital-photo-frame.service

# Start it again
systemctl --user start digital-photo-frame.service

# Or restart
systemctl --user restart digital-photo-frame.service
```

## 10. Test manually

Run the binary manually to see if there are any startup errors:

```bash
# Stop the service first
systemctl --user stop digital-photo-frame.service

# Run manually with environment variables
export DPF_ROOT_PATH=/path/to/photos
export DPF_AWS_PROFILE=your-profile
export DPF_S3_BUCKET=your-bucket
export DPF_TARGET_MAX_DIM=1024
export XDG_RUNTIME_DIR=/run/user/1000
/usr/bin/dpf
```

Look for any error messages in the output.

## Common Issues:

1. **Service not running**: Start it with `systemctl --user start digital-photo-frame.service`
2. **Firewall blocking**: Allow port 8080 with `sudo ufw allow 8080/tcp`
3. **Wrong binary path**: Update the service file `ExecStart` to the correct path
4. **Missing environment variables**: Check and set all required env vars in the service file
5. **Port already in use**: Check if another process is using port 8080
6. **Network issue**: Try accessing by IP address instead of hostname

