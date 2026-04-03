import { contextBridge, ipcRenderer } from 'electron';

contextBridge.exposeInMainWorld('electronAPI', {
  getProjectPath: () => ipcRenderer.invoke('get-project-path'),
  platform: process.platform,
});
