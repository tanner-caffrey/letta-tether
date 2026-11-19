**New: set up channels right in Settings.** Under **Messaging channels**, press **Set up** and pick Telegram, Discord, Slack, WhatsApp or Signal. Letta's own setup asks its questions on the page, one at a time: tokens go in a password box and are never shown again, questions with a default have a **Use default** button, and when it asks which agent to use you pick from your agents by name. No terminal, and it works with the Letta that Letta Tether downloads for you.

**Fixed:** the "which agent answers?" list could be empty if you have a lot of agents.

Not there yet: WhatsApp sets up, but its QR code (to link your phone) doesn't show in Settings yet. Signal is untested.

Already on 0.1.1 or 0.1.2? This installs itself within a day (or press **Update now** in Settings).

**Windows:** download `letta-tether.exe` and run it. Windows will warn that it's from an unknown publisher (it isn't code-signed): **More info → Run anyway**.

**Mac (untested):** download `letta-tether-mac.zip`, unzip, right-click the app → **Open** → **Open**. Please tell us if it works.

**Linux:** `curl -fsSL https://raw.githubusercontent.com/tanner-caffrey/letta-tether/main/install-linux.sh | sh` (already installed? it updates itself.)

`SHA256SUMS.txt` has checksums for every file.
