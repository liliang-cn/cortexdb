// Progressive enhancement only: the page reads the same without it.
(function () {
  var header = document.querySelector(".site-header");
  if (header) {
    var onScroll = function () {
      header.classList.toggle("scrolled", window.scrollY > 8);
    };
    onScroll();
    window.addEventListener("scroll", onScroll, { passive: true });
  }

  var menu = document.querySelector(".menu");
  if (menu) {
    menu.addEventListener("click", function (e) {
      if (e.target.closest("a")) menu.removeAttribute("open");
    });
    document.addEventListener("click", function (e) {
      if (menu.hasAttribute("open") && !menu.contains(e.target)) menu.removeAttribute("open");
    });
    document.addEventListener("keydown", function (e) {
      if (e.key === "Escape") menu.removeAttribute("open");
    });
  }

  document.querySelectorAll("[data-copy]").forEach(function (button) {
    if (!navigator.clipboard) {
      button.hidden = true;
      return;
    }
    var label = button.querySelector("span");
    var idle = label ? label.textContent : "";
    button.addEventListener("click", function () {
      var target = document.getElementById(button.getAttribute("data-copy"));
      if (!target) return;
      navigator.clipboard.writeText(target.innerText.trim()).then(function () {
        button.classList.add("done");
        if (label) label.textContent = button.getAttribute("data-done") || "Copied";
        setTimeout(function () {
          button.classList.remove("done");
          if (label) label.textContent = idle;
        }, 1600);
      });
    });
  });
})();
