// Initialize
updateConnectionStatus();
setInterval(updateConnectionStatus, 1000);
resetPasswordFields();
fetchNodes();
connectWebSocket();
checkAdminAuth();
fetchPublicSettings();
// Periodic fallback polling every 5s
setInterval(fetchNodes, 5000);
// Refresh the site title and favicon when WebSocket is unavailable.
setInterval(fetchPublicSettings, 15000);
