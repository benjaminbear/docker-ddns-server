function deleteHost(id) {
    $.ajax({
        contentType: 'application/x-www-form-urlencoded; charset=UTF-8',
        type: 'GET',
        url: "/hosts/delete/" + id
    }).done(function(data, textStatus, jqXHR) {
        location.href="/hosts";
    }).fail(function(jqXHR, textStatus, errorThrown) {
        alert("Error: " + $.parseJSON(jqXHR.responseText).message);
        location.reload()
    });
}

function addEditHost(id, addedit) {
    if (id == null) {
        id = ""
    } else {
        id = "/"+id
    }

    $.ajax({
        contentType: 'application/x-www-form-urlencoded; charset=UTF-8',
        data: $('#edithostform').serialize(),
        type: 'POST',
        url: '/hosts/'+addedit+id,
    }).done(function(data, textStatus, jqXHR) {
        location.href="/hosts";
    }).fail(function(jqXHR, textStatus, errorThrown) {
        alert("Error: " + $.parseJSON(jqXHR.responseText).message);
    });

    return false;
}

function logOut(){
        try {
            // This is for Firefox
            $.ajax({
                // This can be any path on your same domain which requires HTTPAuth
                url: "",
                username: 'reset',
                password: 'reset',
                // If the return is 401, refresh the page to request new details.
                statusCode: { 401: function() {
                        document.location = document.location;
                    }
                }
            });
        } catch (exception) {
            // Firefox throws an exception since we didn't handle anything but a 401 above
            // This line works only in IE
            if (!document.execCommand("ClearAuthenticationCache")) {
                // exeCommand returns false if it didn't work (which happens in Chrome) so as a last
                // resort refresh the page providing new, invalid details.
                document.location = "http://reset:reset@" + document.location.hostname + document.location.pathname;
            }
        }
}

function randomHash() {
    var chars = "abcdefghijklmnopqrstuvwxyz!@#$%^&*()-+<>ABCDEFGHIJKLMNOP1234567890";
    var pass = "";
    for (var x = 0; x < 32; x++) {
        var i = Math.floor(Math.random() * chars.length);
        pass += chars.charAt(i);
    }
    return pass;
}

function generateUsername() {
    edithostform.username.value = randomHash();
}

function generatePassword() {
    edithostform.password.value = randomHash();
}

function copyToClipboard(inputId) {
    var copyText = document.getElementById(inputId);
    copyText.select();
    copyText.setSelectionRange(0, 99999);
    document.execCommand("copy");
}