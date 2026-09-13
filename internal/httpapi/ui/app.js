// Front end for GET /qr. Nothing from the user or the server is ever written
// as HTML: text goes through textContent, and the image is shown from a blob:
// URL holding exactly the bytes /qr returned.
'use strict';

(() => {
  const form = document.getElementById('form');
  const urlInput = document.getElementById('url');
  const ec = document.getElementById('ec');
  const margin = document.getElementById('margin');
  const fg = document.getElementById('fg');
  const bg = document.getElementById('bg');
  const transparent = document.getElementById('transparent');
  const error = document.getElementById('error');
  const result = document.getElementById('result');
  const frame = document.getElementById('frame');
  const img = document.getElementById('qr');
  const download = document.getElementById('download');

  let objectURL = null;
  let latest = 0;

  // Without this script the browser's own validation and a plain GET to /qr
  // still work; with it, errors are shown inline instead.
  form.noValidate = true;

  // Treat "example.com/x" as "https://example.com/x". Anything that already
  // has a scheme is left alone, so the service can reject it with a reason.
  // A digit after the colon means host:port, not a scheme.
  function normalise(value) {
    const v = value.trim();
    if (v === '' || /^[a-z][a-z0-9+.-]*:(?!\d)/i.test(v)) return v;
    return 'https://' + v;
  }

  function query(url) {
    return new URLSearchParams({
      url,
      ec: ec.value,
      margin: margin.value,
      fg: fg.value,
      bg: transparent.checked ? 'none' : bg.value,
    }).toString();
  }

  function showError(message) {
    error.textContent = message;
    error.hidden = false;
    result.hidden = true;
  }

  async function generate() {
    const url = normalise(urlInput.value);
    urlInput.value = url;
    if (url === '') {
      showError('Enter a URL to encode.');
      urlInput.focus();
      return;
    }

    const id = ++latest;
    let res;
    let blob;
    try {
      res = await fetch('qr?' + query(url));
      if (res.ok) blob = await res.blob();
    } catch {
      if (id === latest) showError('Could not reach the service. Check your connection and try again.');
      return;
    }
    if (id !== latest) return; // superseded by a newer request

    if (!res.ok) {
      let message = `The service answered with status ${res.status}.`;
      try {
        const body = await res.json();
        if (typeof body.message === 'string') message = body.message;
      } catch {
        // Not a JSON error body; keep the generic message.
      }
      if (id === latest) showError(message);
      return;
    }

    if (objectURL) URL.revokeObjectURL(objectURL);
    objectURL = URL.createObjectURL(blob);
    img.src = objectURL;
    img.alt = 'QR code for ' + url;
    download.href = objectURL;
    frame.classList.toggle('transparent', transparent.checked);
    error.hidden = true;
    result.hidden = false;
  }

  form.addEventListener('submit', (e) => {
    e.preventDefault();
    generate();
  });

  // Once a code is showing, keep it in step with the options.
  form.addEventListener('change', (e) => {
    if (e.target !== urlInput && !result.hidden) generate();
  });

  transparent.addEventListener('change', () => {
    bg.disabled = transparent.checked;
  });
})();
