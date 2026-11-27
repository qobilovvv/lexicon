// HTMX owns server interactions. These listeners handle local UI feedback only.
document.addEventListener('click', event => {
  const toggle = event.target.closest('.mobile-nav-toggle');
  if (toggle) {
    const sidebar = document.getElementById('library-sidebar');
    toggle.setAttribute('aria-expanded', String(sidebar.classList.toggle('open')));
  }
});
document.addEventListener('htmx:afterSwap', () => {
  document.querySelector('.server-error')?.focus();
});
for (const name of ['htmx:sendError', 'htmx:responseError']) {
  document.addEventListener(name, () => {
    const error = document.querySelector('.network-error');
    if (error) error.hidden = false;
  });
}
document.addEventListener('htmx:beforeRequest', () => {
  const error = document.querySelector('.network-error');
  if (error) error.hidden = true;
});
