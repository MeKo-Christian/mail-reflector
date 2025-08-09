// Mail Reflector Web Interface JavaScript

// Utility functions
function showMessage(message, type = 'info') {
    const alertDiv = document.createElement('div');
    alertDiv.className = `alert alert-${type}`;
    alertDiv.textContent = message;
    
    // Insert at the top of the main content
    const main = document.querySelector('main');
    main.insertBefore(alertDiv, main.firstChild);
    
    // Auto-remove after 5 seconds
    setTimeout(() => {
        if (alertDiv.parentNode) {
            alertDiv.parentNode.removeChild(alertDiv);
        }
    }, 5000);
}

function showSpinner(button) {
    const originalText = button.textContent;
    button.disabled = true;
    button.innerHTML = '<div class="spinner inline-block mr-2"></div>Loading...';
    
    return () => {
        button.disabled = false;
        button.textContent = originalText;
    };
}

// API functions for dashboard actions
async function testConnection() {
    const button = event.target;
    const hideSpinner = showSpinner(button);
    
    try {
        // This will be implemented in Sprint 1.3 when we add real-time operations
        await new Promise(resolve => setTimeout(resolve, 2000)); // Simulate API call
        showMessage('IMAP connection test not yet implemented - coming in Sprint 1.3!', 'warning');
    } catch (error) {
        showMessage('Failed to test connection: ' + error.message, 'error');
    } finally {
        hideSpinner();
    }
}

async function testSMTP() {
    const button = event.target;
    const hideSpinner = showSpinner(button);
    
    try {
        // This will be implemented in Sprint 1.3 when we add real-time operations
        await new Promise(resolve => setTimeout(resolve, 2000)); // Simulate API call
        showMessage('SMTP connection test not yet implemented - coming in Sprint 1.3!', 'warning');
    } catch (error) {
        showMessage('Failed to test SMTP connection: ' + error.message, 'error');
    } finally {
        hideSpinner();
    }
}

async function runCheck() {
    const button = event.target;
    const hideSpinner = showSpinner(button);
    
    try {
        // This will be implemented in Sprint 1.3 when we add manual trigger functionality
        await new Promise(resolve => setTimeout(resolve, 2000)); // Simulate API call
        showMessage('Manual check trigger not yet implemented - coming in Sprint 1.3!', 'warning');
    } catch (error) {
        showMessage('Failed to run check: ' + error.message, 'error');
    } finally {
        hideSpinner();
    }
}

// Form validation
function validateForm(form) {
    const requiredFields = form.querySelectorAll('[required]');
    let isValid = true;
    
    requiredFields.forEach(field => {
        if (!field.value.trim()) {
            field.classList.add('border-red-500');
            isValid = false;
        } else {
            field.classList.remove('border-red-500');
        }
    });
    
    return isValid;
}

// Auto-focus on login form
document.addEventListener('DOMContentLoaded', function() {
    const usernameField = document.getElementById('username');
    if (usernameField) {
        usernameField.focus();
    }
    
    // Add form validation
    const forms = document.querySelectorAll('form');
    forms.forEach(form => {
        form.addEventListener('submit', function(e) {
            if (!validateForm(form)) {
                e.preventDefault();
                showMessage('Please fill in all required fields', 'error');
            }
        });
    });
    
    // Auto-hide alerts after clicking
    document.addEventListener('click', function(e) {
        if (e.target.classList.contains('alert')) {
            e.target.style.opacity = '0';
            setTimeout(() => {
                if (e.target.parentNode) {
                    e.target.parentNode.removeChild(e.target);
                }
            }, 300);
        }
    });
});

// Keyboard shortcuts
document.addEventListener('keydown', function(e) {
    // Ctrl+Alt+L for logout (when not on login page)
    if (e.ctrlKey && e.altKey && e.key === 'l' && !document.getElementById('username')) {
        const logoutForm = document.querySelector('form[action="/logout"]');
        if (logoutForm) {
            logoutForm.submit();
        }
    }
    
    // Escape to close alerts
    if (e.key === 'Escape') {
        const alerts = document.querySelectorAll('.alert');
        alerts.forEach(alert => {
            alert.style.opacity = '0';
            setTimeout(() => {
                if (alert.parentNode) {
                    alert.parentNode.removeChild(alert);
                }
            }, 300);
        });
    }
});

// Configuration management functions (for Sprint 1.2)
function saveConfig() {
    showMessage('Configuration editing coming in Sprint 1.2!', 'info');
}

function backupConfig() {
    showMessage('Configuration backup coming in Sprint 1.2!', 'info');
}

function restoreConfig() {
    showMessage('Configuration restore coming in Sprint 1.2!', 'info');
}

// Export functions to global scope for onclick handlers
window.testConnection = testConnection;
window.testSMTP = testSMTP;
window.runCheck = runCheck;
window.saveConfig = saveConfig;
window.backupConfig = backupConfig;
window.restoreConfig = restoreConfig;