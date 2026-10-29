// Wrap ALL event handlers in document.ready to ensure DOM is loaded
$(document).ready(function(){

    // ===== HOST BUTTONS =====
    $(document).on("click", "button.addHost", function () {
        location.href='/@/hosts/add';
    });

    $(document).on("click", "button.editHost", function () {
        location.href='/@/hosts/edit/' + $(this).attr('id');
    });

    $(document).on("click", "button.deleteHost", function () {
        $.ajax({
            contentType: 'application/x-www-form-urlencoded; charset=UTF-8',
            type: 'GET',
            url: "/@/hosts/delete/" + $(this).attr('id')
        }).done(function(data, textStatus, jqXHR) {
            if(window.refreshLists) window.refreshLists(); else location.href="/@/hosts";
        }).fail(function(jqXHR, textStatus, errorThrown) {
            alert("Error: " + $.parseJSON(jqXHR.responseText).message);
            location.reload()
        });
    });

    $(document).on("click", "button.showHostLog", function () {
        location.href='/@/logs/host/' + $(this).attr('id');
    });

    // ===== CNAME BUTTONS =====
    $(document).on("click", "button.addCName", function () {
        location.href='/@/cnames/add';
    });

    $(document).on("click", "button.deleteCName", function () {
        $.ajax({
            contentType: 'application/x-www-form-urlencoded; charset=UTF-8',
            type: 'GET',
            url: "/@/cnames/delete/" + $(this).attr('id')
        }).done(function(data, textStatus, jqXHR) {
            location.href="/@/cnames";
        }).fail(function(jqXHR, textStatus, errorThrown) {
            alert("Error: " + $.parseJSON(jqXHR.responseText).message);
            location.reload()
        });
    });

    // ===== ADD/EDIT FORM BUTTONS =====
    $(document).on("click", "button.add, button.edit", function () {
        let id = $(this).attr('id');
        if (id !== "") {
            id = "/"+id
        }

        let action;
        if ($(this).hasClass("add")) {
            action = "add";
        }

        if ($(this).hasClass("edit")) {
            action = "edit";
        }

        let type;
        if ($(this).hasClass("host")) {
            type = "hosts";
        }

        if ($(this).hasClass("cname")) {
            type = "cnames";
        }

        $('#domain').prop('disabled', false);

        $.ajax({
            contentType: 'application/x-www-form-urlencoded; charset=UTF-8',
            data: $('#editHostForm').serialize(),
            type: 'POST',
            url: '/@/'+type+'/'+action+id,
        }).done(function(data, textStatus, jqXHR) {
            location.href="/@/"+type;
        }).fail(function(jqXHR, textStatus, errorThrown) {
            alert("Error: " + $.parseJSON(jqXHR.responseText).message);
        });

        return false;
    });

    // ===== LOGOUT BUTTON =====
    $("#logout").click(function (){
        // Note: The old HTTP Basic Auth logout code is commented out since we use sessions now
        // The logout link goes to /@/logout which handles session destruction
        console.log("Logout clicked - redirecting to /@/logout");
    });

    // ===== CLIPBOARD BUTTONS =====
    $(document).on("click", "button.copyToClipboard", function () {
        let id;
        if ($(this).hasClass('username')) {
            id = "username";
        } else if ($(this).hasClass('password')) {
            id = "password";
        }

        let copyText = document.getElementById(id);
        copyText.select();
        copyText.setSelectionRange(0, 99999);
        document.execCommand("copy");
    });

    $(document).on("click", "button.copyUrlToClipboard", function () {
        let id = $(this).attr('id');
        let hostname = document.getElementById('host-hostname_'+id).innerHTML
        let domain = document.getElementById('host-domain_'+id).innerHTML
        let username = document.getElementById('host-username_'+id).innerHTML
        let password = document.getElementById('host-password_'+id).innerHTML
        let out = location.protocol + '//' +username.trim()+':'+password.trim()+'@'+ domain
        out +='/update?hostname='+hostname

        let dummy = document.createElement("textarea");
        document.body.appendChild(dummy);
        dummy.value = out;
        dummy.select();
        document.execCommand("copy");
        document.body.removeChild(dummy);
    });

    // ===== GENERATE HASH BUTTONS =====
    $(document).on("click", "button.generateHash", function () {
        let id;
        if ($(this).hasClass('username')) {
            id = "username";
        } else if ($(this).hasClass('password')) {
            id = "password";
        }

        let input = document.getElementById(id);
        input.value = randomHash();
    });

    // ===== TOOLTIPS =====
    $(".errorTooltip").tooltip({
        track: true,
        content: function () {
            return $(this).prop('title');
        }
    });

    // ===== NAVIGATION HIGHLIGHTING =====
    urlPath = new URL(window.location.href).pathname.split("/")[2];
    if (urlPath === "") {
        urlPath = "hosts"
    }
    const activeNav = document.getElementsByClassName("nav-"+urlPath)[0];
    if (activeNav) activeNav.classList.add("active");
});

// ===== UTILITY FUNCTIONS (outside document.ready is OK) =====
function newTargetSelected() {
    var sel = document.getElementById("target_id");
    var x = sel.options[sel.selectedIndex].label.replace(sel.options[sel.selectedIndex].text, '');
    document.getElementById("domain_mirror").value = x;
}

function randomHash() {
    let chars = "0123456789abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ";
    var passwordLength = 16;
    var password = "";
    for (var i = 0; i <= passwordLength; i++) {
        var randomNumber = Math.floor(Math.random() * chars.length);
        password += chars.substring(randomNumber, randomNumber +1);
    }
    return password;
}
