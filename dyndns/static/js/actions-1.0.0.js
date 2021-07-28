$("button.addHost").click(function () {
    location.href='/hosts/add';
});

$("button.editHost").click(function () {
    location.href='/hosts/edit/' + $(this).attr('id');
});

$("button.deleteHost").click(function () {
    $.ajax({
        contentType: 'application/x-www-form-urlencoded; charset=UTF-8',
        type: 'GET',
        url: "/hosts/delete/" + $(this).attr('id')
    }).done(function(data, textStatus, jqXHR) {
        location.href="/hosts";
    }).fail(function(jqXHR, textStatus, errorThrown) {
        alert("Error: " + $.parseJSON(jqXHR.responseText).message);
        location.reload()
    });
});

$("button.showHostLog").click(function () {
    location.href='/logs/host/' + $(this).attr('id');
});

$("button.add, button.edit").click(function () {
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
        url: '/'+type+'/'+action+id,
    }).done(function(data, textStatus, jqXHR) {
        location.href="/"+type;
    }).fail(function(jqXHR, textStatus, errorThrown) {
        alert("Error: " + $.parseJSON(jqXHR.responseText).message);
    });

    return false;
});

$("#logout").click(function (){
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
});

$("button.addCName").click(function () {
    location.href='/cnames/add';
});

$("button.deleteCName").click(function () {
    $.ajax({
        contentType: 'application/x-www-form-urlencoded; charset=UTF-8',
        type: 'GET',
        url: "/cnames/delete/" + $(this).attr('id')
    }).done(function(data, textStatus, jqXHR) {
        location.href="/cnames";
    }).fail(function(jqXHR, textStatus, errorThrown) {
        alert("Error: " + $.parseJSON(jqXHR.responseText).message);
        location.reload()
    });
});

function newTargetSelected() {
    var sel = document.getElementById("target_id");
    var x = sel.options[sel.selectedIndex].label.replace(sel.options[sel.selectedIndex].text, '');
    document.getElementById("domain_mirror").value = x;
}

$("button.copyToClipboard").click(function () {
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

function randomHash() {
    let chars = "abcdefghijklmnopqrstuvwxyz!@#$%^&*()-+<>ABCDEFGHIJKLMNOP1234567890";
    let pass = "";
    for (let x = 0; x < 32; x++) {
        let i = Math.floor(Math.random() * chars.length);
        pass += chars.charAt(i);
    }
    return pass;
}

$("button.generateHash").click(function () {
    let id;
    if ($(this).hasClass('username')) {
        id = "username";
    } else if ($(this).hasClass('password')) {
        id = "password";
    }

    let input = document.getElementById(id);
    input.value = randomHash();
});

$(document).ready(function(){
    $(".errorTooltip").tooltip({
        track: true,
        content: function () {
            return $(this).prop('title');
        }
    });

    urlPath = new URL(window.location.href).pathname.split("/")[1];
    if (urlPath === "") {
        urlPath = "hosts"
    }
    document.getElementsByClassName("nav-"+urlPath)[0].classList.add("active");
});
