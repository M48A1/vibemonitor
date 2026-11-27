(() => {
  const { ready, request, status } = window.VibeAdmin;
  const $ = id => document.getElementById(id);

  function loadTimezones(current) {
    const common = ['Asia/Shanghai', 'Asia/Hong_Kong', 'Asia/Tokyo', 'Asia/Singapore', 'Asia/Kolkata',
      'Europe/London', 'Europe/Berlin', 'America/New_York', 'America/Chicago', 'America/Denver',
      'America/Los_Angeles', 'Pacific/Auckland', 'UTC'];
    const browser = typeof Intl.supportedValuesOf === 'function' ? Intl.supportedValuesOf('timeZone') : [];
    const zones = new Set([...common, current, ...browser]);
    $('reportScheduleTimezone').replaceChildren(...Array.from(zones, zone => new Option(zone, zone)));
    $('reportScheduleTimezone').value = current;
  }

  function wire(form, action, success) {
    form.addEventListener('submit', async event => {
      event.preventDefault();
      const button = form.querySelector('button[type="submit"]');
      button.disabled = true;
      status('');
      try {
        status(success(await action()), 'success');
      } catch (error) {
        status(error.message, 'error');
      } finally {
        button.disabled = false;
      }
    });
  }

  function post(url, data) {
    return request(url, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(data)
    });
  }

  ready(async () => {
    wire($('reportManualForm'), () => post('/api/admin/reports/generate', { date: $('reportDate').value }),
      result => result.created ? `${result.date} 日报已存档` : `${result.date} 日报已存档，可直接查询`);
    const schedule = await request('/api/admin/reports/schedule');
    $('reportScheduleEnabled').checked = schedule.enabled;
    $('reportScheduleTime').value = schedule.time;
    loadTimezones(schedule.timezone);
    wire($('reportScheduleForm'), () => post('/api/admin/reports/schedule', {
      enabled: $('reportScheduleEnabled').checked,
      time: $('reportScheduleTime').value,
      timezone: $('reportScheduleTimezone').value
    }), () => '自动生成设置已保存');
  });
})();
