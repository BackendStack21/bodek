// Progressive enhancement: all examples remain visible without JavaScript.
(() => {
  document.querySelectorAll('[data-tabset]').forEach(group => {
    const list = group.querySelector('[role="tablist"]');
    const tabs = Array.from(list.querySelectorAll('[role="tab"]'));
    const panels = tabs.map(tab => document.getElementById(tab.getAttribute('aria-controls')));
    if (panels.some(panel => !panel)) return;
    function select(index, focus = false) {
      tabs.forEach((tab, i) => {
        tab.setAttribute('aria-selected', String(i === index));
        tab.tabIndex = i === index ? 0 : -1;
        panels[i].hidden = i !== index;
      });
      if (focus) tabs[index].focus();
    }
    tabs.forEach((tab, index) => {
      panels[index].setAttribute('role', 'tabpanel');
      panels[index].setAttribute('aria-labelledby', tab.id);
      panels[index].tabIndex = 0;
      tab.addEventListener('click', () => select(index));
      tab.addEventListener('keydown', event => {
        let next;
        if (event.key === 'ArrowRight') next = (index + 1) % tabs.length;
        else if (event.key === 'ArrowLeft') next = (index + tabs.length - 1) % tabs.length;
        else if (event.key === 'Home') next = 0;
        else if (event.key === 'End') next = tabs.length - 1;
        else return;
        event.preventDefault();
        select(next, true);
      });
    });
    select(0);
    list.hidden = false;
  });
  document.querySelectorAll('[data-copy]').forEach(button => {
    button.hidden = false;
    button.addEventListener('click', async () => {
      const code = button.closest('.command').querySelector('code');
      try {
        await navigator.clipboard.writeText(code.textContent);
        button.textContent = 'Copied';
      } catch {
        button.textContent = 'Select code to copy';
      }
      setTimeout(() => { button.textContent = 'Copy'; }, 2000);
    });
  });
})();
