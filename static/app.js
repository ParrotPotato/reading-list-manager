// Progressive enhancement. Every link and form here works on its own as a
// plain request; this just swaps the server-rendered regions in place so
// opening an article or saving notes never reloads the page under you.
(function () {
  "use strict";

  var REGIONS = ["main", "panel-host", "all-tags"];

  function panelOpen() { return !!document.querySelector(".panel"); }

  function panelForm() { return document.querySelector(".panel-form"); }

  // dirty reports whether the panel has edits that are not saved yet. The DOM
  // keeps the server-rendered value in defaultValue, so nothing to track.
  function dirty() {
    var form = panelForm();
    if (!form) return false;
    return Array.prototype.some.call(
      form.querySelectorAll("textarea, input[type=text]"),
      function (el) { return el.value !== el.defaultValue; }
    );
  }

  // apply swaps the regions of the freshly fetched document into this page.
  function apply(html, url, push) {
    var doc = new DOMParser().parseFromString(html, "text/html");
    if (!doc.getElementById("main") || !doc.getElementById("panel-host")) {
      window.location.href = url;
      return false;
    }
    REGIONS.forEach(function (id) {
      var fresh = doc.getElementById(id);
      var current = document.getElementById(id);
      if (fresh && current) current.replaceWith(fresh);
    });
    document.title = doc.title;
    document.body.classList.toggle("panel-open", panelOpen());

    if (push) history.pushState({}, "", url);
    else history.replaceState({}, "", url);
    return true;
  }

  function text(res) {
    return res.text().then(function (html) { return { html: html, url: res.url }; });
  }

  function navigate(url, push) {
    return fetch(url, { credentials: "same-origin" })
      .then(text)
      .then(function (r) { apply(r.html, r.url, push !== false); })
      .catch(function () { window.location.href = url; });
  }

  function send(form) {
    var isPanelSave = form.classList.contains("panel-form");
    var notes = form.querySelector("#notes");
    var caret = notes && document.activeElement === notes ? notes.selectionStart : null;

    // URLSearchParams posts as application/x-www-form-urlencoded, which is what
    // the browser sends for these forms on its own. A raw FormData body would go
    // out as multipart, which net/http's ParseForm does not read.
    var body = new URLSearchParams(new FormData(form));

    return fetch(form.action, { method: "POST", body: body, credentials: "same-origin" })
      .then(text)
      .then(function (r) {
        if (!apply(r.html, r.url, false)) return;
        var fresh = document.getElementById("notes");
        if (fresh && caret !== null) {
          fresh.focus();
          fresh.setSelectionRange(caret, caret);
        }
        if (isPanelSave) flashSaved();
      })
      .catch(function () { form.submit(); });
  }

  function flashSaved() {
    var note = document.querySelector(".panel-form .saved-note");
    if (!note) return;
    note.hidden = false;
    setTimeout(function () { note.hidden = true; }, 1800);
  }

  // leave goes to url, saving the panel's pending edits first so that closing
  // it — by Escape, the close button or a click outside — never loses notes.
  function leave(url) {
    if (!dirty()) return navigate(url, true);
    var form = panelForm();
    var back = form.querySelector("input[name=return]");
    if (back) back.value = url;
    return send(form);
  }

  function focusPanel() {
    var heading = document.querySelector(".panel h2");
    if (!heading) return;
    heading.setAttribute("tabindex", "-1");
    heading.focus({ preventScroll: true });
  }

  // Plain left clicks on local links are handled here; modified clicks and
  // external links are left to the browser.
  document.addEventListener("click", function (e) {
    if (e.defaultPrevented || e.button !== 0 || e.metaKey || e.ctrlKey || e.shiftKey || e.altKey) return;
    var link = e.target.closest("a[data-swap]");
    if (!link || link.origin !== window.location.origin) return;
    e.preventDefault();
    var href = link.getAttribute("href");
    var done = panelOpen() ? leave(href) : navigate(href, true);
    if (link.classList.contains("notes-link")) done.then(focusPanel);
  });

  document.addEventListener("submit", function (e) {
    var form = e.target;
    e.preventDefault();
    if (form.method.toLowerCase() === "get") {
      var params = new URLSearchParams(new FormData(form));
      navigate(form.getAttribute("action") + "?" + params.toString(), true);
      return;
    }
    send(form);
  });

  document.addEventListener("change", function (e) {
    if (e.target.matches("select[data-autosubmit]")) {
      send(e.target.form);
    } else if (e.target.matches("select[data-kind]")) {
      relabelCreator(e.target);
    }
  });

  // A book has an author, a film a director, a video a channel. The chosen
  // category decides what the creator field is called.
  function relabelCreator(select) {
    var option = select.options[select.selectedIndex];
    var form = select.closest("form");
    if (!option || !form) return;
    var label = form.querySelector("[data-creator-label]");
    if (label) label.textContent = option.dataset.creator;
  }

  document.addEventListener("keydown", function (e) {
    if (e.key === "Escape" && panelOpen()) {
      var close = document.querySelector(".panel-close");
      if (close) leave(close.getAttribute("href"));
    }
    // Cmd/Ctrl+Enter saves the notes you are typing.
    if ((e.metaKey || e.ctrlKey) && e.key === "Enter" && e.target.id === "notes") {
      e.preventDefault();
      send(e.target.form);
    }
  });

  window.addEventListener("popstate", function () { navigate(window.location.href, false); });
})();
