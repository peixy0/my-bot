export const settingKey = "myBotBrowserSettings";
export const groupColors = ["blue", "red", "yellow", "green", "pink", "purple", "cyan", "orange", "grey"];
export const protocolVersion = 1;
export const maxFrameSize = 1024 * 1024;
export const maxTextLength = 64 * 1024;
export const reconnectInitialDelay = 1000;
export const reconnectMaxDelay = 30000;
export const maxAXNodes = 1000;
export const maxNetworkEntries = 200;

export const ICONS_ON = {
  16: "icons/robot-on-16.png",
  32: "icons/robot-on-32.png",
  48: "icons/robot-on-48.png",
  128: "icons/robot-on-128.png",
};
export const ICONS_OFF = {
  16: "icons/robot-off-16.png",
  32: "icons/robot-off-32.png",
  48: "icons/robot-off-48.png",
  128: "icons/robot-off-128.png",
};

export const keyCodes: Record<string, number> = {
  Enter: 13,
  Tab: 9,
  Escape: 27,
  Backspace: 8,
  Delete: 46,
  ArrowDown: 40,
  ArrowUp: 38,
  ArrowLeft: 37,
  ArrowRight: 39,
  PageDown: 34,
  PageUp: 33,
  Home: 36,
  End: 35,
  Space: 32,
  Control: 17,
  Shift: 16,
  Alt: 18,
  Meta: 91,
};
