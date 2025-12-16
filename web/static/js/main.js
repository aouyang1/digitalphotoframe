function openPhotoModal(imageUrl) {
    const modal = document.getElementById('photo-modal');
    const modalImg = document.getElementById('photo-modal-img');
    modalImg.src = imageUrl;
    modal.classList.add('active');
    document.body.style.overflow = 'hidden';
}

function closePhotoModal() {
    const modal = document.getElementById('photo-modal');
    modal.classList.remove('active');
    document.body.style.overflow = 'auto';
}

// Close modal on Escape key
document.addEventListener('keydown', function(event) {
    if (event.key === 'Escape') {
        closePhotoModal();
    }
});

function switchView(viewName, button) {
    const views = document.querySelectorAll('.view');
    views.forEach(function(view) {
        view.classList.remove('active-view');
    });

    const target = document.getElementById('view-' + viewName);
    if (target) {
        target.classList.add('active-view');
    }

    const navItems = document.querySelectorAll('.nav-item');
    navItems.forEach(function(item) {
        item.classList.remove('active');
    });

    if (button) {
        button.classList.add('active');
    }
}

function playFromPhoto(button) {
    const encodedName = button.dataset.name;
    const category = parseInt(button.dataset.category, 10);
    if (!encodedName || Number.isNaN(category)) {
        console.error('Missing photo metadata for playFromPhoto');
        return;
    }
    const photoName = decodeURIComponent(encodedName);

    fetch('/slideshow/play', {
        method: 'POST',
        headers: {
            'Content-Type': 'application/json'
        },
        body: JSON.stringify({
            photo_name: photoName,
            category: category,
            interval: 0
        })
    }).then(response => {
        if (!response.ok) {
            return response.json().then(data => {
                const msg = data && data.error ? data.error : 'Failed to start slideshow';
                alert(msg);
            }).catch(() => {
                alert('Failed to start slideshow');
            });
        }
    }).catch(() => {
        alert('Failed to start slideshow');
    });
}

// Settings state
let originalSettings = null;
let currentSettings = null;

// Display state for slideshow view
let currentDisplayEnabled = null;

function loadSettings() {
    fetch('/settings')
        .then(response => {
            if (!response.ok) {
                throw new Error('Failed to load settings');
            }
            return response.json();
        })
        .then(data => {
            originalSettings = {
                slideshow_interval_seconds: data.slideshow_interval_seconds,
                include_surprise: data.include_surprise,
                shuffle_enabled: data.shuffle_enabled
            };
            currentSettings = { ...originalSettings };
            applySettingsToUI(currentSettings);
            updateSettingsSaveButton();
        })
        .catch(err => {
            console.error(err);
            const statusEl = document.getElementById('settings-status');
            if (statusEl) {
                statusEl.textContent = 'Failed to load settings';
                statusEl.classList.remove('success');
                statusEl.classList.add('error');
                statusEl.style.display = 'inline';
            }
        });
}

function applySettingsToUI(settings) {
    const intervalInput = document.getElementById('interval-value');
    const intervalUnit = document.getElementById('interval-unit');
    const includeBtn = document.getElementById('toggle-include-surprise');
    const shuffleBtn = document.getElementById('toggle-shuffle');

    if (!intervalInput || !intervalUnit || !includeBtn || !shuffleBtn) {
        return;
    }

    const totalSeconds = settings.slideshow_interval_seconds || 15;
    let value = totalSeconds;
    let unit = 'seconds';

    if (totalSeconds % 3600 === 0) {
        unit = 'hours';
        value = totalSeconds / 3600;
    } else if (totalSeconds % 60 === 0) {
        unit = 'minutes';
        value = totalSeconds / 60;
    }

    intervalInput.value = value;
    intervalUnit.value = unit;

    setToggleButton(includeBtn, settings.include_surprise);
    setToggleButton(shuffleBtn, settings.shuffle_enabled);
}

function setToggleButton(btn, isOn) {
    if (!btn) return;
    btn.dataset.value = isOn ? 'true' : 'false';
    if (isOn) {
        btn.classList.add('toggle-on');
        btn.classList.remove('toggle-off');
    } else {
        btn.classList.add('toggle-off');
        btn.classList.remove('toggle-on');
    }
}

function toggleSettingButton(btn) {
    const current = btn.dataset.value === 'true';
    const next = !current;
    setToggleButton(btn, next);

    if (!currentSettings) {
        currentSettings = { ...originalSettings };
    }

    if (btn.id === 'toggle-include-surprise') {
        currentSettings.include_surprise = next;
    } else if (btn.id === 'toggle-shuffle') {
        currentSettings.shuffle_enabled = next;
    }

    updateSettingsSaveButton();
}

function onIntervalChanged() {
    const intervalInput = document.getElementById('interval-value');
    const intervalUnit = document.getElementById('interval-unit');
    if (!intervalInput || !intervalUnit) return;

    let value = parseInt(intervalInput.value, 10);
    if (Number.isNaN(value) || value < 1) {
        value = 1;
        intervalInput.value = value;
    }

    const unit = intervalUnit.value;
    let seconds = value;
    if (unit === 'minutes') {
        seconds = value * 60;
    } else if (unit === 'hours') {
        seconds = value * 3600;
    }

    if (!currentSettings) {
        currentSettings = { ...originalSettings };
    }
    currentSettings.slideshow_interval_seconds = seconds;
    updateSettingsSaveButton();
}

function updateSettingsSaveButton() {
    const saveBtn = document.getElementById('settings-save-btn');
    if (!saveBtn) return;

    if (!originalSettings || !currentSettings) {
        saveBtn.disabled = true;
        return;
    }

    const changed = JSON.stringify(originalSettings) !== JSON.stringify(currentSettings);
    saveBtn.disabled = !changed;
}

function saveSettings() {
    if (!currentSettings) return;

    const statusEl = document.getElementById('settings-status');
    if (statusEl) {
        statusEl.textContent = 'Saving...';
        statusEl.classList.remove('error', 'success');
        statusEl.style.display = 'inline';
    }

    const payload = {
        slideshow_interval_seconds: currentSettings.slideshow_interval_seconds,
        include_surprise: !!currentSettings.include_surprise,
        shuffle_enabled: !!currentSettings.shuffle_enabled
    };

    if (payload.slideshow_interval_seconds < 1) {
        payload.slideshow_interval_seconds = 1;
    }

    fetch('/settings', {
        method: 'PUT',
        headers: {
            'Content-Type': 'application/json'
        },
        body: JSON.stringify(payload)
    })
        .then(response => {
            if (!response.ok) {
                return response.json().then(data => {
                    const msg = data && data.error ? data.error : 'Failed to save settings';
                    throw new Error(msg);
                }).catch(() => {
                    throw new Error('Failed to save settings');
                });
            }
            return response.json();
        })
        .then(data => {
            originalSettings = {
                slideshow_interval_seconds: data.slideshow_interval_seconds,
                include_surprise: data.include_surprise,
                shuffle_enabled: data.shuffle_enabled
            };
            currentSettings = { ...originalSettings };
            applySettingsToUI(currentSettings);
            updateSettingsSaveButton();

            if (statusEl) {
                statusEl.textContent = 'Saved';
                statusEl.classList.remove('error');
                statusEl.classList.add('success');
                statusEl.style.display = 'inline';
            }
        })
        .catch(err => {
            console.error(err);
            if (statusEl) {
                statusEl.textContent = err.message || 'Failed to save settings';
                statusEl.classList.remove('success');
                statusEl.classList.add('error');
                statusEl.style.display = 'inline';
            }
        });
}

function applyDisplayToUI(enabled) {
    const btn = document.getElementById('toggle-display');
    if (!btn) return;
    setToggleButton(btn, !!enabled);
    currentDisplayEnabled = !!enabled;
}

function loadDisplayState() {
    fetch('/display')
        .then(response => {
            if (!response.ok) {
                throw new Error('Failed to load display state');
            }
            return response.json();
        })
        .then(data => {
            applyDisplayToUI(data.enabled);
        })
        .catch(err => {
            console.error(err);
        });
}

function toggleDisplay(btn) {
    if (currentDisplayEnabled === null) {
        // State not yet loaded; attempt to load then exit.
        loadDisplayState();
        return;
    }

    const previous = currentDisplayEnabled;
    const next = !previous;
    setToggleButton(btn, next);
    currentDisplayEnabled = next;
    btn.disabled = true;

    const desired = next ? 1 : 0;

    fetch('/display/' + desired, {
        method: 'PUT'
    })
        .then(response => {
            if (!response.ok) {
                return response.json().then(data => {
                    const msg = data && data.error ? data.error : 'Failed to update display';
                    throw new Error(msg);
                }).catch(() => {
                    throw new Error('Failed to update display');
                });
            }
            return response.json();
        })
        .then(data => {
            applyDisplayToUI(data.enabled);
        })
        .catch(err => {
            console.error(err);
            alert(err.message || 'Failed to update display');
            // Revert UI to previous state
            applyDisplayToUI(previous);
        })
        .finally(() => {
            btn.disabled = false;
        });
}

document.addEventListener('DOMContentLoaded', function() {
    const intervalInput = document.getElementById('interval-value');
    const intervalUnit = document.getElementById('interval-unit');
    if (intervalInput) {
        intervalInput.addEventListener('change', onIntervalChanged);
        intervalInput.addEventListener('input', onIntervalChanged);
    }
    if (intervalUnit) {
        intervalUnit.addEventListener('change', onIntervalChanged);
    }

    loadSettings();
    loadDisplayState();
});