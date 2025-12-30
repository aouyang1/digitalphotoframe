# digitalphotoframe
personal digital photo frame setup for raspberry pi

## Requirements

### System Dependencies

- **imv** - Image viewer for Wayland (required for slideshow)
- **imgp** - Image processing tool (required for image rotation)

Install on Debian/Ubuntu:
```bash
sudo apt-get install imv-wayland imgp
```

### AWS Setup

1. **Install AWS CLI**
   ```bash
   # On macOS
   brew install awscli
   
   # On Linux
   curl "https://awscli.amazonaws.com/awscli-exe-linux-x86_64.zip" -o "awscliv2.zip"
   unzip awscliv2.zip
   sudo ./aws/install
   ```

2. **Create AWS Profile**
   ```bash
   aws configure --profile <your-profile-name>
   ```
   Enter your AWS Access Key ID, Secret Access Key, region, and output format when prompted.

3. **IAM User Policy**
   
   Create an IAM user with the following policy. Replace the placeholders:
   - `{date}` - Current date (e.g., "2012-10-17")
   - `{region}` - AWS region (e.g., "us-east-1")
   - `{accesspoint_id}` - S3 Access Point ID (if using access points)
   - `{accesspoint_name}` - S3 Access Point name (if using access points)
   - `{s3_bucket_name}` - Your S3 bucket name

   ```json
   {
       "Version": "2012-10-17",
       "Statement": [
           {
               "Sid": "VisualEditor0",
               "Effect": "Allow",
               "Action": [
                   "s3:GetObject",
                   "s3:ListBucket"
               ],
               "Resource": [
                   "arn:aws:s3:{region}:{accesspoint_id}:accesspoint/{accesspoint_name}/object/*",
                   "arn:aws:s3:{region}:{accesspoint_id}:accesspoint/{accesspoint_name}",
                   "arn:aws:s3:::{s3_bucket_name}",
                   "arn:aws:s3:::{s3_bucket_name}/*"
               ]
           }
       ]
   }
   ```

   **Note:** If you're not using S3 Access Points, you can simplify the policy to:
   ```json
   {
       "Version": "2012-10-17",
       "Statement": [
           {
               "Sid": "VisualEditor0",
               "Effect": "Allow",
               "Action": [
                   "s3:GetObject",
                   "s3:ListBucket"
               ],
               "Resource": [
                   "arn:aws:s3:::{s3_bucket_name}",
                   "arn:aws:s3:::{s3_bucket_name}/*"
               ]
           }
       ]
   }
   ```

### Environment Variables

Set the following environment variables before running the application:

- **`DPF_ROOT_PATH`** (Required)
  - Root directory path for storing photos and database
  - Example: `export DPF_ROOT_PATH=/home/user/photos`

- **`DPF_AWS_PROFILE`** (Required)
  - AWS CLI profile name to use for S3 access
  - Example: `export DPF_AWS_PROFILE=my-photo-frame-profile`

- **`DPF_S3_BUCKET`** (Required)
  - S3 bucket name containing photos
  - Example: `export DPF_S3_BUCKET=my-photo-bucket`

### Go Requirements

- Go 1.24.5 or later
- Dependencies will be automatically downloaded via `go mod tidy`

## Running the Application

1. Set environment variables:
   ```bash
   export ROOT_PATH_DPF=/path/to/photos
   export DPF_AWS_PROFILE=your-profile-name
   export DPF_S3_BUCKET=your-bucket-name
   ```

2. Install Go dependencies:
   ```bash
   go mod tidy
   ```

3. Build and run:
   ```bash
   go run .
   ```

   Or build the binary:
   ```bash
   go build -o dpf .
   ./dpf
   ```

4. Access the web UI on local machine:
   - Open `http://localhost` in your browser
   - Or from another device on your network: `http://<your-ip>`

5. Build and upload to pi
   ```bash
   make build && scp dpf {USER}@{PI-HOSTNAME}:/home/{USER}/dpf
   ```
   On the pi
   ```bash
   sudo mv dpf /usr/bin/ && sudo setcap 'cap_net_bind_service=+ep' /usr/bin/dpf
   ```
   setcap is needed as we are publishing the webapp on port 80 on the pi
   
6. Setting up as systemd service
   - create systemd directory for user if not already done
   ```bash
   mkdir -p ~/.config/systemd/user
   cp digital-photo-frame.service ~/.config/systemd/user/
   ```
   - update the digital-photo-frame.service with your own setting
   - enable and start the systemd service
   ```bash
   systemctl --user daemon-reload
   systemctl --user enable digital-photo-frame.service
   systemctl --user start digital-photo-frame.service
   ```
   - can try rebooting to ensure on restart the app load automatically. The desktop will still load but soon after the slideshow should start.
   ```bash
   sudo reboot
   ```
