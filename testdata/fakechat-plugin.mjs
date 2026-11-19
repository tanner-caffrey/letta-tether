// A fake messaging channel for testing Letta Tether. No network: it talks through files.
//   io/<account>.inbox   test writes one JSON message per line: {"chatId","senderId","text"}
//   io/<account>.outbox  everything the channel "sends" (replies, pairing codes) is appended here
//   io/<account>.events  started / stopped
// config.mode = "fail" makes start() throw, to test a broken channel.
import fs from "node:fs";
import path from "node:path";
import { randomUUID } from "node:crypto";

const IO = process.env.FAKECHAT_IO || "/tmp/chtest/io";
const note = (acct, file, obj) => fs.appendFileSync(path.join(IO, `${acct}.${file}`), JSON.stringify({ at: new Date().toISOString(), ...obj }) + "\n");

export const channelPlugin = {
  metadata: { id: "fakechat", displayName: "Fake chat (Tether test)", runtimePackages: [], runtimeModules: [] },
  async createAdapter(account) {
    const acct = account.accountId;
    let timer = null, offset = 0, running = false;
    const adapter = {
      id: `fakechat:${acct}`, channelId: "fakechat", accountId: acct, name: account.displayName ?? "Fake chat",
      async start() {
        if (account.config?.mode === "fail") { note(acct, "events", { event: "start-failed" }); throw new Error("fakechat: told to fail"); }
        const inbox = path.join(IO, `${acct}.inbox`);
        fs.writeFileSync(inbox, "", { flag: "a" });
        offset = fs.statSync(inbox).size;
        running = true;
        note(acct, "events", { event: "started", pid: process.pid });
        timer = setInterval(async () => {
          const size = fs.statSync(inbox).size;
          if (size <= offset) return;
          const fd = fs.openSync(inbox, "r"); const buf = Buffer.alloc(size - offset);
          fs.readSync(fd, buf, 0, buf.length, offset); fs.closeSync(fd); offset = size;
          for (const line of buf.toString().split("\n").filter(Boolean)) {
            const m = JSON.parse(line);
            note(acct, "events", { event: "inbound", chatId: m.chatId });
            await adapter.onMessage?.({ channel: "fakechat", accountId: acct, chatId: m.chatId, senderId: m.senderId,
              senderName: m.senderId, text: m.text, messageId: randomUUID(), timestamp: new Date().toISOString() });
          }
        }, 300);
      },
      async stop() { clearInterval(timer); running = false; note(acct, "events", { event: "stopped" }); },
      isRunning() { return running; },
      async sendMessage(msg) { note(acct, "outbox", { chatId: msg.chatId, text: msg.text }); return { messageId: randomUUID() }; },
      async sendDirectReply(chatId, text) { note(acct, "outbox", { chatId, text, direct: true }); },
      onMessage: undefined,
    };
    return adapter;
  },
  messageActions: {
    describeMessageTool() { return { actions: ["send"] }; },
    async handleAction({ adapter, request, formatText }) {
      const f = formatText(request.message ?? "");
      const r = await adapter.sendMessage({ chatId: request.chatId, text: f.text });
      return `Message sent to fakechat (message_id: ${r.messageId})`;
    },
  },
};
